package ta2a_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/testkit"
	"github.com/nexssp/transportai/ta2a"
)

type mockAgent struct {
	failCard   bool
	failSend   bool
	failGet    bool
	failCancel bool
}

func (m mockAgent) Card(context.Context) (ta2a.AgentCard, error) {
	if m.failCard {
		return ta2a.AgentCard{}, xerr.Internal("card failed")
	}
	return ta2a.AgentCard{
		Name:               "custom-agent",
		Version:            "9.9",
		Description:        "Mock custom agent description",
		URL:                "https://agent.example.com",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []string{"math", "translate"},
	}, nil
}

func (m mockAgent) Send(_ context.Context, msg ta2a.Message) (ta2a.Task, error) {
	if m.failSend {
		return ta2a.Task{}, xerr.BadRequest("custom send failed")
	}
	return ta2a.Task{
		ID:        "custom-task",
		ContextID: msg.ContextID,
		Status:    ta2a.TaskStatusCompleted,
		State:     "completed",
		Text:      "custom",
	}, nil
}

func (m mockAgent) Get(_ context.Context, id string) (ta2a.Task, error) {
	if m.failGet {
		return ta2a.Task{}, xerr.Internal("database error during get")
	}
	if id != "custom-task" {
		return ta2a.Task{}, xerr.NotFound("task not found: " + id)
	}
	return ta2a.Task{ID: id, Status: ta2a.TaskStatusCompleted, State: "completed", Text: "custom"}, nil
}

func (m mockAgent) Cancel(_ context.Context, id string) error {
	if m.failCancel {
		return xerr.Forbidden("cannot cancel task")
	}
	if id != "custom-task" {
		return xerr.NotFound("task not found: " + id)
	}
	return nil
}

type TextOnlyDoc struct {
	Content string
}

func (t TextOnlyDoc) MarshalText() ([]byte, error) {
	return []byte(t.Content), nil
}

type FailingMarshaler struct{}

func (FailingMarshaler) MarshalText() ([]byte, error) {
	return nil, errors.New("marshaling failed")
}

type CustomStringer struct {
	Val string
}

func (c CustomStringer) String() string {
	return "Stringer: " + c.Val
}

func assistantAction() action.AnyAction {
	return action.New("assistant.reply", func(_ context.Context, msg ta2a.Message) (string, error) {
		return "Hello: " + msg.Text, nil
	}).Route(ta2a.Role("assistant")).Build()
}

func hitlAction() action.AnyAction {
	return action.New("hitl.agent", func(_ context.Context, msg ta2a.Message) (ta2a.Task, error) {
		if msg.Text == "start" {
			return ta2a.Task{
				Status: ta2a.TaskStatusInputRequired,
				Text:   "Please approve refund amount",
				Artifacts: []ta2a.Artifact{
					{Name: "RefundForm", Type: "form", Data: map[string]any{"amount": 100}},
				},
			}, nil
		}
		return ta2a.Task{
			Status: ta2a.TaskStatusCompleted,
			Text:   "Approved: " + msg.Text,
		}, nil
	}).Route(ta2a.Role("hitl")).Build()
}

func directTaskWithEmptyStatusAction() action.AnyAction {
	return action.New("direct.task.empty.status", func(_ context.Context, msg ta2a.Message) (ta2a.Task, error) {
		return ta2a.Task{
			Status: "",
			Text:   "AutoStatus: " + msg.Text,
		}, nil
	}).Route(ta2a.Role("empty-status")).Build()
}

func TestAgentCard_Discovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		agent    ta2a.Agent
		wantName string
	}{
		{"default role agent", nil, "nexss-a2a-agent"},
		{"custom agent override", mockAgent{}, "custom-agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := ta2a.New(":0", tt.agent)
			suite := testkit.NewWithHandler(t, srv.Handler())

			suite.GET("/.well-known/agent-card.json").
				Do().
				ExpectOK().
				HasField("name", tt.wantName)
		})
	}
}

func TestAgentCard_WithAgentCardOptionOverridesCustomAgent(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", mockAgent{},
		ta2a.WithAgentCard(ta2a.AgentCard{
			Name:        "overriding-card-agent",
			Description: "Customized card overriding mockAgent",
			Version:     "3.0.0",
		}),
	)
	srv.Mount([]action.AnyAction{assistantAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.GET("/.well-known/agent-card.json").
		Do().
		ExpectOK().
		HasField("name", "overriding-card-agent").
		HasField("version", "3.0.0")
}

func TestDirectTask_EmptyStatusNormalization(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{directTaskWithEmptyStatusAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	var task ta2a.Task
	suite.POST("/message/send", map[string]any{
		"message": map[string]any{"role": "empty-status", "text": "normalize_me"},
	}).
		Do().
		ExpectOK().
		Into(&task)

	if task.Status != ta2a.TaskStatusCompleted || task.State != "completed" {
		t.Fatalf("expected status 'completed', got status=%q state=%q", task.Status, task.State)
	}
	if task.Text != "AutoStatus: normalize_me" {
		t.Fatalf("unexpected text: %s", task.Text)
	}
}

func TestPartsValidation_RejectsEmptyPartsArray(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.POST("/message/send", map[string]any{
		"message": map[string]any{
			"role":  "assistant",
			"text":  "",
			"parts": []map[string]any{{"type": "text", "text": "  "}},
		},
	}).
		Do().
		ExpectBadRequest().
		ExpectErrorKind(xerr.KindBadRequest, "message text or parts is required")
}

func TestCustomAgent_CancelFallbackOnGetError(t *testing.T) {
	t.Parallel()

	custom := mockAgent{failGet: true}
	srv := ta2a.New(":0", custom)
	suite := testkit.NewWithHandler(t, srv.Handler())

	var canceled ta2a.Task
	suite.POST("/tasks/cancel", map[string]any{"id": "custom-task"}).
		Do().
		ExpectOK().
		Into(&canceled)

	if canceled.ID != "custom-task" || canceled.Status != ta2a.TaskStatusCanceled || canceled.State != "canceled" {
		t.Fatalf("expected fallback canceled task, got: %+v", canceled)
	}
}

func TestAuth_AllOperationalEndpoints(t *testing.T) {
	t.Parallel()

	verifier := ta2a.AuthVerifierFunc(func(ctx context.Context, r *http.Request) (context.Context, error) {
		if r.Header.Get("Authorization") != "Bearer valid-agent-key" {
			return nil, xerr.Unauthorized("invalid agent credentials")
		}
		return ctx, nil
	})

	srv := ta2a.New(":0", nil, ta2a.WithAuth(verifier))
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.GET("/.well-known/agent-card.json").Do().ExpectOK()

	suite.POST("/message/send", map[string]any{"message": map[string]any{"role": "assistant", "text": "hi"}}).
		Do().ExpectUnauthorized()
	suite.POST("/message/send", map[string]any{"message": map[string]any{"role": "assistant", "text": "hi"}}).
		WithHeader("Authorization", "Bearer valid-agent-key").
		Do().ExpectOK()

	suite.POST("/message/stream", map[string]any{"message": map[string]any{"role": "assistant", "text": "hi"}}).
		Do().ExpectUnauthorized()

	suite.POST("/tasks/get", map[string]any{"id": "task-1"}).
		Do().ExpectUnauthorized()

	suite.POST("/tasks/cancel", map[string]any{"id": "task-1"}).
		Do().ExpectUnauthorized()
}

func TestWebhook_DeliveryAndHMAC(t *testing.T) {
	t.Parallel()

	var delivered atomic.Int32
	var receivedSig atomic.Pointer[string]

	const secret = "super-secret-hmac-key"

	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sig := r.Header.Get("X-A2A-Signature")
		receivedSig.Store(&sig)

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		if sig != expectedSig {
			t.Errorf("HMAC mismatch: got %q want %q", sig, expectedSig)
		}

		delivered.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	srv := ta2a.New(":0", nil,
		ta2a.WithWebhookSecret(secret),
		ta2a.WithWebhookHTTPClient(webhookServer.Client()),
	)
	srv.Mount([]action.AnyAction{assistantAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	suite.POST("/message/send", map[string]any{
		"message": map[string]any{
			"role":        "assistant",
			"text":        "webhook_test",
			"callbackUrl": webhookServer.URL,
		},
	}).Do().ExpectOK()

	time.Sleep(100 * time.Millisecond)

	if delivered.Load() < 1 {
		t.Fatal("expected webhook to be delivered")
	}
}

func TestMessageSend_HITL_And_Resumption(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{hitlAction()})
	suite := testkit.NewWithHandler(t, srv.Handler())

	var task ta2a.Task
	suite.POST("/message/send", map[string]any{
		"message": map[string]any{
			"role":      "hitl",
			"text":      "start",
			"contextId": "ctx-100",
		},
	}).
		Do().
		ExpectOK().
		Into(&task)

	if task.Status != ta2a.TaskStatusInputRequired {
		t.Fatalf("expected input-required status, got: %s", task.Status)
	}

	var resumedTask ta2a.Task
	suite.POST("/message/send", map[string]any{
		"message": map[string]any{
			"role":      "hitl",
			"text":      "confirmed_by_human",
			"contextId": "ctx-100",
		},
	}).
		Do().
		ExpectOK().
		Into(&resumedTask)

	if resumedTask.ID != task.ID {
		t.Fatalf("expected task ID %q on resumption, got %q", task.ID, resumedTask.ID)
	}
	if resumedTask.Status != ta2a.TaskStatusCompleted {
		t.Fatalf("expected task to be completed, got %s", resumedTask.Status)
	}
}

func TestTransport_ServerLifecycleAndGuard(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := srv.Do(ctx, nil); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected nil or context.Canceled on pre-canceled startup, got: %v", err)
	}

	if _, err := srv.Do(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "already started") {
		t.Fatalf("expected 'already started' error on repeated Do(), got: %v", err)
	}
}

func TestTransport_ConcurrentUniqueTaskIDs(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":0", nil)
	srv.Mount([]action.AnyAction{assistantAction()})

	suite := testkit.NewWithHandler(t, srv.Handler())

	const workers = 30
	var wg sync.WaitGroup
	wg.Add(workers)

	var ids sync.Map
	var duplicates atomic.Int32

	for i := 0; i < workers; i++ {
		go func(n int) {
			defer wg.Done()

			var tsk ta2a.Task
			suite.POST("/message/send", map[string]any{
				"message": map[string]any{"role": "assistant", "text": fmt.Sprintf("ping_%d", n)},
			}).
				Do().
				ExpectOK().
				Into(&tsk)

			if _, exists := ids.LoadOrStore(tsk.ID, true); exists {
				duplicates.Add(1)
			}

			var fetched ta2a.Task
			suite.POST("/tasks/get", map[string]any{"id": tsk.ID}).
				Do().
				ExpectOK().
				Into(&fetched)

			if fetched.Text != fmt.Sprintf("Hello: ping_%d", n) {
				t.Errorf("task data mismatch: expected 'Hello: ping_%d', got %q", n, fetched.Text)
			}
		}(i)
	}

	wg.Wait()

	if duplicates.Load() > 0 {
		t.Fatalf("detected %d duplicate task IDs during concurrent execution", duplicates.Load())
	}
}

func TestFormatAgentResult_TableDriven(t *testing.T) {
	t.Parallel()

	type CustomStruct struct {
		Name  string `json:"name"`
		Score int    `json:"score"`
	}

	ptrVal := "pointer value"
	var nilPtr *string

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"nil value", nil, ""},
		{"string value", "plain text", "plain text"},
		{"*string non-nil", &ptrVal, "pointer value"},
		{"*string nil", nilPtr, ""},
		{"[]byte value", []byte("byte slice"), "byte slice"},
		{"TextMarshaler", TextOnlyDoc{Content: "doc content"}, "doc content"},
		{"Stringer", CustomStringer{Val: "custom"}, "Stringer: custom"},
		{"Failing TextMarshaler fallback", FailingMarshaler{}, "{}"},
		{"Struct JSON fallback", CustomStruct{Name: "Alice", Score: 100}, "{\n  \"name\": \"Alice\",\n  \"score\": 100\n}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actionWithOutput := action.New("custom.out", func(_ context.Context, _ struct{}) (any, error) {
				return tt.input, nil
			}).Route(ta2a.Role("result-test")).Build()

			srv := ta2a.New(":0", nil)
			srv.Mount([]action.AnyAction{actionWithOutput})

			suite := testkit.NewWithHandler(t, srv.Handler())

			var task ta2a.Task
			suite.POST("/message/send", map[string]any{
				"message": map[string]any{"role": "result-test", "text": "run"},
			}).
				Do().
				ExpectOK().
				Into(&task)

			if strings.TrimSpace(task.Text) != strings.TrimSpace(tt.expected) {
				t.Fatalf("formatAgentResult() mismatch:\n got:  %q\n want: %q", task.Text, tt.expected)
			}
		})
	}
}
