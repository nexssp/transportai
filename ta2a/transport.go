package ta2a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport"
)

type a2aStartKey struct{}

type Transport struct {
	addr          string
	agent         Agent
	cardProvider  AgentCardProvider
	authVerifier  AuthVerifier
	webhookSecret string
	webhookClient *http.Client

	actions map[string]action.AnyAction
	mu      sync.RWMutex

	tasks          map[string]Task
	tasksByContext map[string]string // contextID -> taskID (O(1) HITL index)
	taskMu         sync.RWMutex
	taskSeq        atomic.Uint64

	server       *http.Server
	serverMu     sync.Mutex
	mux          *http.ServeMux
	mdws         []func(http.Handler) http.Handler
	mdwsMu       sync.RWMutex
	maxBodyBytes int64

	started bool
	startMu sync.Mutex
}

var _ transport.Transport = (*Transport)(nil)

func New(addr string, agent Agent, opts ...Option) *Transport {
	if addr != "" && !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	t := &Transport{
		addr:           addr,
		actions:        make(map[string]action.AnyAction),
		tasks:          make(map[string]Task),
		tasksByContext: make(map[string]string),
		mux:            http.NewServeMux(),
		maxBodyBytes:   1 << 20, // 1 MiB default limit
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

				a.AddAnyHook(action.AnyHook{
					Before: func(ctx context.Context, req any, meta *action.Meta) (context.Context, error) {
						return context.WithValue(ctx, a2aStartKey{}, time.Now()), nil
					},
					After: func(ctx context.Context, req any, res any, err error, meta *action.Meta) {
						start, _ := ctx.Value(a2aStartKey{}).(time.Time)
						slog.InfoContext(ctx, "a2a_agent_call",
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
		return nil, fmt.Errorf("a2a server already started")
	}
	t.started = true
	t.startMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

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

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutCtx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return nil, fmt.Errorf("a2a server crashed: %w", err)
	}

	return nil, nil
}
