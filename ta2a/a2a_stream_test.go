package ta2a_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/testkit"
	"github.com/nexssp/transportai/ta2a"
)

func streamArtifactAction() action.AnyAction {
	return action.New("report.gen", func(_ context.Context, msg ta2a.Message) (ta2a.Task, error) {
		return ta2a.Task{
			Status: ta2a.TaskStatusCompleted,
			Text:   "Report ready for " + msg.Text,
			Artifacts: []ta2a.Artifact{
				{Name: "SummaryPDF", Type: "file", MimeType: "application/pdf", URL: "https://files.example.com/summary.pdf"},
			},
		}, nil
	}).Route(ta2a.Role("reporter")).Build()
}

func liveTokenYieldingAction() action.AnyAction {
	return action.New("llm.stream", func(ctx context.Context, msg ta2a.Message) (string, error) {
		_ = ta2a.YieldToken(ctx, "Token_1 ")
		_ = ta2a.YieldToken(ctx, "Token_2 ")
		_ = ta2a.YieldToken(ctx, "Token_3")
		return "Full Response", nil
	}).Route(ta2a.Role("llm")).Build()
}

func failingStreamAction() action.AnyAction {
	return action.New("failing.stream", func(_ context.Context, _ ta2a.Message) (string, error) {
		return "", xerr.NotFound("database record not found")
	}).Route(ta2a.Role("failing")).Build()
}

func emptyResultStreamAction() action.AnyAction {
	return action.New("empty.stream", func(_ context.Context, _ ta2a.Message) (string, error) {
		return "", nil
	}).Route(ta2a.Role("empty")).Build()
}

func TestA2A_SSE_Streaming_LiveTokenYielding(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{liveTokenYieldingAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	req := httptest.NewRequest("POST", "/message/stream", strings.NewReader(`{
		"message": {
			"role": "llm",
			"text": "prompt"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	stream := suite.ListenSSEWithRequest(t, req)
	defer stream.Close()

	// 1. Initial working event
	statusEvt := stream.WaitFor(t, "status", 2*time.Second)
	if !strings.Contains(statusEvt.Data, `"status":"working"`) {
		t.Fatalf("expected initial working event, got %q", statusEvt.Data)
	}

	// 2. Progressive live token chunks
	chunk1 := stream.WaitFor(t, "chunk", 2*time.Second)
	if !strings.Contains(chunk1.Data, "Token_1") {
		t.Fatalf("expected Token_1, got: %s", chunk1.Data)
	}

	// 3. Final completion event
	completeEvt := stream.WaitFor(t, "complete", 2*time.Second)
	if !strings.Contains(completeEvt.Data, `"status":"completed"`) {
		t.Fatalf("expected complete event, got %q", completeEvt.Data)
	}
}

func TestA2A_SSE_Streaming_ProgressAndChunks(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{streamArtifactAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	req := httptest.NewRequest("POST", "/message/stream", strings.NewReader(`{
		"message": {
			"role": "reporter",
			"text": "Quarter_3"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	stream := suite.ListenSSEWithRequest(t, req)
	defer stream.Close()

	statusEvt := stream.WaitFor(t, "status", 2*time.Second)
	if !strings.Contains(statusEvt.Data, `"status":"working"`) {
		t.Fatalf("expected initial working event, got %q", statusEvt.Data)
	}

	chunkEvt := stream.WaitFor(t, "chunk", 2*time.Second)
	if !strings.Contains(chunkEvt.Data, "Report ready for Quarter_3") {
		t.Fatalf("expected progress chunk event, got %q", chunkEvt.Data)
	}

	artifactEvt := stream.WaitFor(t, "artifact", 2*time.Second)
	if !strings.Contains(artifactEvt.Data, "SummaryPDF") {
		t.Fatalf("expected SummaryPDF artifact event, got %q", artifactEvt.Data)
	}

	completeEvt := stream.WaitFor(t, "complete", 2*time.Second)
	if !strings.Contains(completeEvt.Data, `"status":"completed"`) {
		t.Fatalf("expected complete event, got %q", completeEvt.Data)
	}
}

func TestA2A_SSE_Streaming_ActionError(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{failingStreamAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	req := httptest.NewRequest("POST", "/message/stream", strings.NewReader(`{
		"message": {
			"role": "failing",
			"text": "query"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	stream := suite.ListenSSEWithRequest(t, req)
	defer stream.Close()

	errEvt := stream.WaitFor(t, "error", 2*time.Second)
	if !strings.Contains(errEvt.Data, `"status":"failed"`) || !strings.Contains(errEvt.Data, "record not found") {
		t.Fatalf("expected failed status with error details, got: %s", errEvt.Data)
	}
}

func TestA2A_SSE_Streaming_EmptyResult(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{emptyResultStreamAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	req := httptest.NewRequest("POST", "/message/stream", strings.NewReader(`{
		"message": {
			"role": "empty",
			"text": "query"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	stream := suite.ListenSSEWithRequest(t, req)
	defer stream.Close()

	statusEvt := stream.WaitFor(t, "status", 2*time.Second)
	if !strings.Contains(statusEvt.Data, `"status":"working"`) {
		t.Fatalf("expected status event, got: %s", statusEvt.Data)
	}

	completeEvt := stream.WaitFor(t, "complete", 2*time.Second)
	if !strings.Contains(completeEvt.Data, `"status":"completed"`) {
		t.Fatalf("expected complete event, got: %s", completeEvt.Data)
	}
}

func TestA2A_Stream_Validation(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{streamArtifactAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.POST("/message/stream", map[string]any{
		"message": map[string]any{"role": "reporter", "text": ""},
	}).
		Do().
		ExpectBadRequest().
		ExpectErrorKind(xerr.KindBadRequest, "message text or parts is required")

	suite.POST("/message/stream", map[string]any{
		"message": map[string]any{"role": "", "text": "Data"},
	}).
		Do().
		ExpectBadRequest().
		ExpectErrorKind(xerr.KindBadRequest, "message role is required")
}
