package ta2a_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/testkit"
	"github.com/nexssp/transport"
	"github.com/nexssp/transportai/ta2a"
)

func TestAgentCard_Default(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.GET("/.well-known/agent-card.json").
		Do().
		ExpectOK().
		HasField("name", "nexss-a2a-agent").
		HasField("version", "1.0.0")
}

func TestAgentCard_CustomAgent(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", mockAgent{})
	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.GET("/.well-known/agent-card.json").
		Do().
		ExpectOK().
		HasField("name", "custom-agent").
		HasField("version", "9.9").
		HasField("url", "https://agent.example.com")
}

func TestMessageSend_RoleAgent(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	var task ta2a.Task

	suite.POST("/message/send", map[string]any{
		"message": map[string]any{
			"role": "assistant",
			"text": "Hi",
		},
	}).
		WithHeader(transport.HeaderRequestID, "req-send-ok").
		Do().
		ExpectOK().
		Into(&task)

	if task.ID == "" {
		t.Fatal("expected task ID")
	}
	if task.Status != ta2a.TaskStatusCompleted {
		t.Fatalf("unexpected status: %s", task.Status)
	}
	if !strings.Contains(task.Text, "Hello: Hi") {
		t.Fatalf("unexpected task text: %s", task.Text)
	}
}

func TestMessageSend_PartsSupport(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	var task ta2a.Task

	suite.POST("/message/send", map[string]any{
		"message": map[string]any{
			"role": "assistant",
			"parts": []map[string]any{
				{"type": "text", "text": "Part1"},
				{"type": "text", "text": "Part2"},
			},
		},
	}).
		Do().
		ExpectOK().
		Into(&task)

	if !strings.Contains(task.Text, "Hello: Part1 Part2") {
		t.Fatalf("expected parts to be normalized into text, got %q", task.Text)
	}
}

func TestTask_EmptyAndMalformedRequests(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	// 1. Missing ID on /tasks/get -> 400
	suite.POST("/tasks/get", map[string]any{"id": ""}).
		Do().
		ExpectBadRequest().
		ExpectErrorKind(xerr.KindBadRequest, "task id is required")

	// 2. Missing ID on /tasks/cancel -> 400
	suite.POST("/tasks/cancel", map[string]any{"id": ""}).
		Do().
		ExpectBadRequest().
		ExpectErrorKind(xerr.KindBadRequest, "task id is required")

	// 3. Missing / empty message on /message/send -> 400
	suite.POST("/message/send", map[string]any{}).
		Do().
		ExpectBadRequest().
		ExpectErrorKind(xerr.KindBadRequest, "message role is required")
}

func TestHTTPMethodNotAllowedAndUnknownPath(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/.well-known/agent-card.json", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /.well-known/agent-card.json, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/unknown/endpoint", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown endpoint, got %d", rec.Code)
	}
}

func TestUnsupportedContentType(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{assistantAction()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send",
		strings.NewReader(`{"message":{"role":"assistant","text":"hi"}}`))
	req.Header.Set("Content-Type", "text/plain")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for text/plain, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMaxBodyBytesBoundary(t *testing.T) {
	t.Parallel()

	const limit = 256
	srv := ta2a.New(":0", nil, ta2a.WithMaxBodyBytes(limit))
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	// Payload within limit passes (200 OK)
	small := strings.Repeat("a", 20)
	suite.POST("/message/send", map[string]any{
		"message": map[string]any{"role": "assistant", "text": small},
	}).
		Do().
		ExpectOK()

	// Payload exceeding limit fails (413 Payload Too Large)
	large := strings.Repeat("a", limit*2)
	suite.POST("/message/send", map[string]any{
		"message": map[string]any{"role": "assistant", "text": large},
	}).
		Do().
		ExpectStatus(http.StatusRequestEntityTooLarge)
}
