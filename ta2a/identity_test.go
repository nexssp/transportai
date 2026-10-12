package ta2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/transport/identity"
)

func TestTransportSendPopulatesCorrelationContext(t *testing.T) {
	t.Parallel()
	const traceID = "trace-inbound-42"
	act := action.New("identity.echo", func(ctx context.Context, _ struct{}) (string, error) {
		return xctx.TraceIDFrom(ctx), nil
	}).Route(Role("identity.echo")).Build()
	transport := New(":0", nil)
	transport.Mount([]action.AnyAction{act})

	task, err := transport.Send(context.Background(), Message{
		Role: "identity.echo",
		Text: "trace",
		Metadata: map[string]any{
			identity.HeaderTraceID: traceID,
		},
	})
	if err != nil {
		t.Fatalf("send A2A message: %v", err)
	}
	if task.Text != traceID {
		t.Fatalf("action trace ID = %q, want %q", task.Text, traceID)
	}
}

func TestClientSendPropagatesCorrelationAndMessageMetadata(t *testing.T) {
	t.Parallel()
	const traceID = "trace-outbound-84"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(identity.HeaderTraceID); got != traceID {
			t.Errorf("HTTP trace header = %q, want %q", got, traceID)
		}
		var request struct {
			Message Message `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode A2A request: %v", err)
			return
		}
		if got := request.Message.Metadata[identity.HeaderTraceID]; got != traceID {
			t.Errorf("message trace metadata = %v, want %q", got, traceID)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(Task{ID: "task-1", Status: TaskStatusCompleted}); err != nil {
			t.Errorf("encode A2A task: %v", err)
		}
	}))
	defer server.Close()

	ctx := xctx.WithTraceID(context.Background(), traceID)
	if _, err := NewClient(server.URL).Send(ctx, Message{Role: "remote.echo"}); err != nil {
		t.Fatalf("send remote A2A message: %v", err)
	}
}

func TestUntrustedMessageMetadataCannotSetTenantIdentity(t *testing.T) {
	t.Parallel()
	msg := Message{Metadata: map[string]any{identity.HeaderTenantID: "untrusted-tenant"}}
	ctx := populateFromMessage(context.Background(), msg)
	if got := xctx.TenantIDFrom(ctx); got != "" {
		t.Fatalf("untrusted message metadata set tenant identity to %q", got)
	}
}
