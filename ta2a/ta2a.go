package ta2a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport"
	"golang.org/x/sync/errgroup"
)

type a2aStartKey struct{}

type pendingApprovalEntry struct {
	msg       Message
	createdAt time.Time
}

type Transport struct {
	addr          string
	agent         Agent
	cardProvider  AgentCardProvider
	authVerifier  AuthVerifier
	webhookSecret string
	webhookClient *http.Client

	actions  map[string]action.AnyAction
	bindings map[string]AgentBinding
	mu       sync.RWMutex

	tasks            map[string]Task
	tasksByContext   map[string]string
	pendingApprovals map[string]pendingApprovalEntry
	taskMu           sync.RWMutex
	taskSeq          atomic.Uint64
	taskTTL          time.Duration

	server       *http.Server
	serverMu     sync.Mutex
	mux          *http.ServeMux
	mdws         []func(http.Handler) http.Handler
	mdwsMu       sync.RWMutex
	maxBodyBytes int64
	logger       *slog.Logger
	traceCalls   bool

	started bool
	startMu sync.Mutex
}

var _ transport.Transport = (*Transport)(nil)

func New(addr string, agent Agent, opts ...Option) *Transport {
	if addr != "" && !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	t := &Transport{
		addr:             addr,
		actions:          make(map[string]action.AnyAction),
		bindings:         make(map[string]AgentBinding),
		tasks:            make(map[string]Task),
		tasksByContext:   make(map[string]string),
		pendingApprovals: make(map[string]pendingApprovalEntry),
		mux:              http.NewServeMux(),
		maxBodyBytes:     1 << 20,
		taskTTL:          24 * time.Hour,
		logger:           slog.Default(),
	}

	if agent != nil {
		t.agent = agent
	} else {
		t.agent = t
	}

	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}

	t.setupRoutes()
	return t
}

func (t *Transport) CanHandle(b action.Binding) bool {
	_, ok := b.(AgentBinding)
	return ok
}

func (t *Transport) String() string {
	return "a2a(" + t.addr + ")"
}

func (t *Transport) Mount(actions []action.AnyAction) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, a := range actions {
		if a == nil {
			continue
		}

		for _, b := range a.GetBindings() {
			if ab, ok := b.(AgentBinding); ok && ab.Role != "" {
				t.actions[ab.Role] = a
				t.bindings[ab.Role] = ab

				if !t.traceCalls {
					continue
				}

				a.AddAnyHook(action.AnyHook{
					Before: func(ctx context.Context, _ any, _ *action.Meta) (context.Context, error) {
						return context.WithValue(ctx, a2aStartKey{}, time.Now()), nil
					},
					After: func(ctx context.Context, _, _ any, err error, meta *action.Meta) {
						start, _ := ctx.Value(a2aStartKey{}).(time.Time)
						t.logger.InfoContext(ctx, "a2a_agent_call",
							"role", ab.Role,
							"action", meta.Name,
							"duration_ms", time.Since(start).Milliseconds(),
							"error", err,
						)
					},
				})
			}
		}
	}
}

func (t *Transport) Use(mw ...func(http.Handler) http.Handler) *Transport {
	t.mdwsMu.Lock()
	defer t.mdwsMu.Unlock()
	t.mdws = append(t.mdws, mw...)
	return t
}

func (t *Transport) AsAction() *action.Builder[any, any] {
	return action.New("transport.a2a."+t.addr, func(ctx context.Context, _ any) (any, error) {
		return t.Do(ctx, nil)
	}).
		Tag("infra", "transport", "a2a").
		Description("Agent2Agent Server on " + t.addr)
}

func (t *Transport) Handler() http.Handler {
	t.mdwsMu.RLock()
	mdwsCopy := append([]func(http.Handler) http.Handler(nil), t.mdws...)
	t.mdwsMu.RUnlock()

	var h http.Handler = t.mux
	for _, m := range slices.Backward(mdwsCopy) {
		h = m(h)
	}
	return h
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	t.startMu.Lock()
	if t.started {
		t.startMu.Unlock()
		return nil, errors.New("a2a server already started")
	}
	if err := ctx.Err(); err != nil {
		t.started = true
		t.startMu.Unlock()
		return nil, err
	}
	if _, err := loadHTTPBearerToken(); err != nil {
		t.startMu.Unlock()
		return nil, fmt.Errorf("a2a HTTP authentication is not configured: %w", err)
	}
	t.started = true
	t.startMu.Unlock()

	t.serverMu.Lock()
	t.server = &http.Server{
		Addr:              t.addr,
		Handler:           t.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	server := t.server
	t.serverMu.Unlock()

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", t.addr)
	if err != nil {
		return nil, fmt.Errorf("a2a server failed to listen: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = listener.Close()
		return nil, err
	}
	contracts.SignalReady(ctx)

	group, groupCtx := errgroup.WithContext(ctx)
	if t.taskTTL > 0 {
		group.Go(func() error {
			t.runCleanupLoop(groupCtx)
			return nil
		})
	}
	group.Go(func() error {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("a2a server crashed: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		select {
		case <-ctx.Done():
		case <-groupCtx.Done():
			if ctx.Err() == nil {
				return nil
			}
		}

		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutCtx); err != nil {
			t.logger.WarnContext(shutCtx, "a2a_http_shutdown_failed", "error", err)
			if closeErr := server.Close(); closeErr != nil {
				return errors.Join(
					fmt.Errorf("a2a server shutdown failed: %w", err),
					fmt.Errorf("a2a server close failed: %w", closeErr),
				)
			}
			return fmt.Errorf("a2a server shutdown failed: %w", err)
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}

	return nil, ctx.Err()
}

func (t *Transport) runCleanupLoop(ctx context.Context) {
	interval := min(max(t.taskTTL/2, 10*time.Millisecond), 10*time.Minute)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			t.sweepExpiredTasks(now.UTC())
		}
	}
}

func (t *Transport) sweepExpiredTasks(now time.Time) {
	t.taskMu.Lock()
	defer t.taskMu.Unlock()

	if t.taskTTL <= 0 {
		return
	}

	for id := range t.tasks {
		tsk := t.tasks[id]
		if len(tsk.Transitions) > 0 {
			lastActivity := tsk.Transitions[len(tsk.Transitions)-1].Timestamp
			if now.Sub(lastActivity) > t.taskTTL {
				delete(t.tasks, id)
				if tsk.ContextID != "" {
					delete(t.tasksByContext, tsk.ContextID)
					delete(t.pendingApprovals, tsk.ContextID)
				}
			}
		}
	}

	for key, entry := range t.pendingApprovals {
		if now.Sub(entry.createdAt) > t.taskTTL {
			delete(t.pendingApprovals, key)
		}
	}
}
