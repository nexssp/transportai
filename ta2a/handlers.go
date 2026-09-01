package ta2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"sync"

	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
)

type maxBytesError struct {
	cause error
}

func (e maxBytesError) Error() string { return e.cause.Error() }
func (e maxBytesError) Unwrap() error { return e.cause }

func (t *Transport) setupRoutes() {
	t.mux.HandleFunc("GET /.well-known/agent-card.json", t.handleAgentCard)
	t.mux.HandleFunc("POST /message/send", t.handleSend)
	t.mux.HandleFunc("POST /message/stream", t.handleStream)
	t.mux.HandleFunc("POST /tasks/get", t.handleGet)
	t.mux.HandleFunc("POST /tasks/cancel", t.handleCancel)
}

func (t *Transport) authenticate(ctx context.Context, r *http.Request) (context.Context, error) {
	if t.authVerifier == nil {
		return ctx, nil
	}
	return t.authVerifier.Verify(ctx, r)
}

func (t *Transport) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	ctx, scope, release := xctx.NewScope(r.Context())
	defer release()

	scope.Endpoint = "/.well-known/agent-card.json"
	scope.RequestID = r.Header.Get(transport.HeaderRequestID)

	var card AgentCard
	var err error

	switch {
	case t.cardProvider != nil:
		card, err = t.cardProvider.AgentCard(ctx)
	case t.agent != nil:
		card, err = t.agent.Card(ctx)
	default:
		card, err = t.Card(ctx)
	}

	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, card)
}

func (t *Transport) handleSend(w http.ResponseWriter, r *http.Request) {
	ctx, scope, release := xctx.NewScope(r.Context())
	defer release()

	scope.Endpoint = "/message/send"
	scope.RequestID = r.Header.Get(transport.HeaderRequestID)

	var authErr error
	ctx, authErr = t.authenticate(ctx, r)
	if authErr != nil {
		writeError(w, r, authErr)
		return
	}

	var req struct {
		Message Message `json:"message"`
	}

	if err := t.decodeBody(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	if err := req.Message.Validate(); err != nil {
		writeError(w, r, err)
		return
	}

	task, err := t.agent.Send(ctx, req.Message)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (t *Transport) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx, scope, release := xctx.NewScope(r.Context())
	defer release()

	scope.Endpoint = "/message/stream"
	scope.RequestID = r.Header.Get(transport.HeaderRequestID)

	var authErr error
	ctx, authErr = t.authenticate(ctx, r)
	if authErr != nil {
		writeError(w, r, authErr)
		return
	}

	var req struct {
		Message Message `json:"message"`
	}

	if err := t.decodeBody(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	if err := req.Message.Validate(); err != nil {
		writeError(w, r, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, xerr.Internal("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var taskID string
	if req.Message.ContextID != "" {
		t.taskMu.RLock()
		if id, found := t.tasksByContext[req.Message.ContextID]; found {
			taskID = id
		} else if _, ok := t.tasks[req.Message.ContextID]; ok {
			taskID = req.Message.ContextID
		}
		t.taskMu.RUnlock()
	}
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", t.taskSeq.Add(1))
	}

	if req.Message.Metadata == nil {
		req.Message.Metadata = make(map[string]any)
	}
	req.Message.Metadata["task_id"] = taskID

	initialTask := Task{
		ID:        taskID,
		ContextID: req.Message.ContextID,
		Status:    TaskStatusWorking,
		State:     string(TaskStatusWorking),
	}
	writeSSEEvent(w, flusher, "status", initialTask)

	var tokensStreamed bool
	streamCtx := WithStreamYield(ctx, func(chunk string) error {
		tokensStreamed = true
		writeSSEEvent(w, flusher, "chunk", map[string]string{
			"id":    taskID,
			"delta": chunk,
		})
		return nil
	})

	var streamedArtifacts sync.Map
	streamCtx = WithStreamArtifactYield(streamCtx, func(art Artifact) error {
		streamedArtifacts.Store(art.Name, true)
		writeSSEEvent(w, flusher, "artifact", map[string]any{
			"taskId":   taskID,
			"artifact": art,
		})
		return nil
	})

	task, err := t.agent.Send(streamCtx, req.Message)

	if err != nil {
		appErr := xerr.From(err)
		errTask := Task{
			ID:        taskID,
			ContextID: req.Message.ContextID,
			Status:    TaskStatusFailed,
			State:     string(TaskStatusFailed),
			Error:     &TaskError{Code: string(appErr.Kind), Message: appErr.Message},
		}
		writeSSEEvent(w, flusher, "error", errTask)
		return
	}

	if !tokensStreamed && task.Text != "" {
		writeSSEEvent(w, flusher, "chunk", map[string]string{
			"id":   task.ID,
			"text": task.Text,
		})
	}

	for _, art := range task.Artifacts {
		if _, already := streamedArtifacts.Load(art.Name); !already {
			writeSSEEvent(w, flusher, "artifact", map[string]any{
				"taskId":   task.ID,
				"artifact": art,
			})
		}
	}

	writeSSEEvent(w, flusher, "complete", task)
}

func (t *Transport) handleGet(w http.ResponseWriter, r *http.Request) {
	ctx, scope, release := xctx.NewScope(r.Context())
	defer release()

	scope.Endpoint = "/tasks/get"
	scope.RequestID = r.Header.Get(transport.HeaderRequestID)

	var authErr error
	ctx, authErr = t.authenticate(ctx, r)
	if authErr != nil {
		writeError(w, r, authErr)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := t.decodeBody(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	if req.ID == "" {
		writeError(w, r, xerr.BadRequest("task id is required"))
		return
	}

	task, err := t.agent.Get(ctx, req.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func (t *Transport) handleCancel(w http.ResponseWriter, r *http.Request) {
	ctx, scope, release := xctx.NewScope(r.Context())
	defer release()

	scope.Endpoint = "/tasks/cancel"
	scope.RequestID = r.Header.Get(transport.HeaderRequestID)

	var authErr error
	ctx, authErr = t.authenticate(ctx, r)
	if authErr != nil {
		writeError(w, r, authErr)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := t.decodeBody(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}

	if req.ID == "" {
		writeError(w, r, xerr.BadRequest("task id is required"))
		return
	}

	if err := t.agent.Cancel(ctx, req.ID); err != nil {
		writeError(w, r, err)
		return
	}

	task, err := t.agent.Get(ctx, req.ID)
	if err != nil {
		slog.WarnContext(ctx, "a2a_cancel_get_after_cancel_failed", "task_id", req.ID, "error", err)
		task = Task{
			ID:     req.ID,
			Status: TaskStatusCanceled,
			State:  string(TaskStatusCanceled),
		}
	}

	writeJSON(w, http.StatusOK, task)
}

func (t *Transport) decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return xerr.BadRequest("Content-Type is required")
	}

	mt, _, err := mime.ParseMediaType(ct)
	if err != nil || mt != "application/json" {
		return xerr.BadRequest("Content-Type must be application/json")
	}

	r.Body = http.MaxBytesReader(w, r.Body, t.maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(target); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return maxBytesError{cause: err}
		}
		return xerr.BadRequest("invalid JSON body", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return xerr.BadRequest("invalid JSON body: unexpected trailing data")
	}

	return nil
}

func writeSSEEvent(w io.Writer, flusher http.Flusher, eventName string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, b)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr maxBytesError
	if errors.As(err, &maxErr) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(xerr.ErrorResponse{
			Error:     string(xerr.KindTooManyRequests),
			Message:   "request body too large",
			RequestID: r.Header.Get(transport.HeaderRequestID),
		})
		return
	}

	appErr := xerr.From(err)
	status := transport.MapToHTTPStatus(appErr.Kind)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(appErr.Public(r.Header.Get(transport.HeaderRequestID)))
}
