package tmcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/thttp"
)

func (t *Transport) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /mcp/sse", t.serveSSE)
	mux.HandleFunc("POST /mcp/message", t.serveHTTPMessage)

	return mux
}

// HTTPRoutes returns transport actions for any router that understands
// thttp.RawHandler bindings.
func (t *Transport) HTTPRoutes() []action.AnyAction {
	return []action.AnyAction{
		action.New[any, any]("mcp.sse", nil).
			Description("MCP SSE Transport").
			Route(thttp.RawHandler(http.MethodGet, "/mcp/sse", t.serveSSE)).
			Build(),

		action.New[any, any]("mcp.message", nil).
			Description("MCP Message Endpoint").
			Route(thttp.RawHandler(http.MethodPost, "/mcp/message", t.serveHTTPMessage)).
			Build(),
	}
}

func (t *Transport) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan Response, 16)

	t.mu.Lock()
	t.sessions[sessionID] = ch
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		if current, ok := t.sessions[sessionID]; ok && current == ch {
			delete(t.sessions, sessionID)
		}
		t.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	fmt.Fprintf(w, "event: endpoint\ndata: /mcp/message?sessionId=%s\n\n", sessionID)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return

		case resp, open := <-ch:
			if !open {
				return
			}

			data, err := json.Marshal(resp)
			if err != nil {
				continue
			}

			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (t *Transport) serveHTTPMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorResponse(nil, CodeParseError, "Malformed JSON payload"))
		return
	}

	resp := t.dispatch(r.Context(), req)

	// No SSE session: answer over plain HTTP.
	if sessionID == "" {
		if req.ID != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		return
	}

	t.mu.RLock()
	ch, ok := t.sessions[sessionID]
	t.mu.RUnlock()

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown or expired session"}`))
		return
	}

	// Notifications do not produce responses.
	if req.ID != nil {
		select {
		case ch <- resp:
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"error":"session delivery timeout"}`))
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}
