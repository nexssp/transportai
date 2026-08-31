package ta2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
)

type AgentCard struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	URL          string          `json:"url,omitempty"`
	Version      string          `json:"version,omitempty"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
}

type Message struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	ContextID string `json:"contextId,omitempty"`
}

type Task struct {
	ID        string `json:"id"`
	ContextID string `json:"contextId,omitempty"`
	State     string `json:"state"`
	Text      string `json:"text,omitempty"`
}

type Agent interface {
	Card(context.Context) (AgentCard, error)
	Send(context.Context, Message) (Task, error)
	Get(context.Context, string) (Task, error)
	Cancel(context.Context, string) error
}

type Transport struct {
	addr         string
	agent        Agent
	actions      map[string]action.AnyAction
	server       *http.Server
	mux          *http.ServeMux
	mdws         []func(http.Handler) http.Handler
	maxBodyBytes int64
	mu           sync.RWMutex
}

var _ transport.Transport = (*Transport)(nil)

func New(addr string, agent Agent) *Transport {
	if addr != "" && !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	t := &Transport{
		addr:         addr,
		agent:        agent,
		actions:      make(map[string]action.AnyAction),
		mux:          http.NewServeMux(),
		maxBodyBytes: 1 << 20, // 1 MiB
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
		for _, b := range a.GetBindings() {
			if ab, ok := b.(AgentBinding); ok {
				t.actions[ab.Role] = a
			}
		}
	}
}

func (t *Transport) AsAction() *action.Builder[any, any] {
	return action.New("transport.a2a."+t.addr, func(ctx context.Context, _ any) (any, error) {
		return t.Do(ctx, nil)
	}).
		Tag("infra", "transport", "a2a").
		Description("Agent2Agent Server on " + t.addr)
}

func (t *Transport) Handler() http.Handler {
	var h http.Handler = t.mux
	for _, m := range slices.Backward(t.mdws) {
		h = m(h)
	}
	return h
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	t.server = &http.Server{
		Addr:              t.addr,
		Handler:           t.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = t.server.Shutdown(shutCtx)
	}()

	if err := t.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return nil, fmt.Errorf("a2a server crashed: %w", err)
	}
	return nil, nil
}

func (t *Transport) setupRoutes() {
	t.mux.HandleFunc("GET /.well-known/agent-card.json", t.handleAgentCard)
	t.mux.HandleFunc("POST /message/send", t.handleSend)
	t.mux.HandleFunc("POST /tasks/get", t.handleGet)
	t.mux.HandleFunc("POST /tasks/cancel", t.handleCancel)
}

func (t *Transport) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if t.agent == nil {
		http.Error(w, `{"error":"agent unavailable"}`, http.StatusInternalServerError)
		return
	}
	card, err := t.agent.Card(r.Context())
	if err != nil {
		http.Error(w, `{"error":"agent card unavailable"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (t *Transport) handleSend(w http.ResponseWriter, r *http.Request) {
	if t.agent == nil {
		http.Error(w, `{"error":"agent unavailable"}`, http.StatusInternalServerError)
		return
	}
	var req struct {
		Message Message `json:"message"`
	}
	tooLarge, err := decodeJSONBody(w, r, t.maxBodyBytes, &req)
	if tooLarge {
		http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil || req.Message.Text == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	task, err := t.agent.Send(r.Context(), req.Message)
	if err != nil {
		appErr := xerr.From(err)
		w.WriteHeader(transport.MapToHTTPStatus(appErr.Kind))
		_ = json.NewEncoder(w).Encode(appErr.Public(""))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (t *Transport) handleGet(w http.ResponseWriter, r *http.Request) {
	if t.agent == nil {
		http.Error(w, `{"error":"agent unavailable"}`, http.StatusInternalServerError)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, t.maxBodyBytes)).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	task, err := t.agent.Get(r.Context(), req.ID)
	if err != nil {
		appErr := xerr.From(err)
		w.WriteHeader(transport.MapToHTTPStatus(appErr.Kind))
		_ = json.NewEncoder(w).Encode(appErr.Public(""))
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (t *Transport) handleCancel(w http.ResponseWriter, r *http.Request) {
	if t.agent == nil {
		http.Error(w, `{"error":"agent unavailable"}`, http.StatusInternalServerError)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, t.maxBodyBytes)).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if err := t.agent.Cancel(r.Context(), req.ID); err != nil {
		appErr := xerr.From(err)
		w.WriteHeader(transport.MapToHTTPStatus(appErr.Kind))
		_ = json.NewEncoder(w).Encode(appErr.Public(""))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": req.ID, "state": "canceled"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// decodeJSONBody decodes r.Body into target, enforcing t.maxBodyBytes.
// Returns a *ta2a-appropriate boolean for "body exceeded the size limit" so callers
// can return 413 instead of a generic 400.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBodyBytes int64, target any) (tooLarge bool, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err = json.NewDecoder(r.Body).Decode(target)
	if err == nil {
		return false, nil
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true, err
	}
	return false, err
}
