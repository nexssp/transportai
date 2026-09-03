package ta2a_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/nexssp/transportai/ta2a"
)

type SampleReq struct {
	Profile    string `json:"profile"`
	Editor     string `json:"editor"`
	OutputFile string `json:"output_file"`
	MaxLines   int    `json:"max_lines"`
	Force      bool   `json:"force"`
}

type SampleRes struct {
	Profile    string `json:"profile"`
	Content    string `json:"content"`
	Summary    string `json:"summary"`
	FileCount  int    `json:"file_count"`
	FuncCount  int    `json:"func_count"`
	TotalBytes int64  `json:"total_bytes"`
}

func (s SampleRes) String() string {
	return s.Summary
}

func TestTA2A_DecoderPrecedence_Hierarchy(t *testing.T) {
	t.Parallel()

	var capturedReq SampleReq

	testAct := action.New("test.precedence", func(ctx context.Context, req SampleReq) (*SampleRes, error) {
		capturedReq = req
		return &SampleRes{
			Profile:   req.Profile,
			Content:   "Generated Payload",
			Summary:   "Processed successfully",
			FileCount: 10,
			FuncCount: 50,
		}, nil
	}).
		Route(
			ta2a.Role("test-role").
				WithDefaultArgs(SampleReq{
					Profile:    "default-profile",
					Editor:     "markdown",
					OutputFile: "default.md",
					MaxLines:   100,
				}).
				WithArgs(map[string]any{
					"output_file": "HARD_OVERRIDE.md",
				}),
		).
		Build()

	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{testAct})

	ctx := context.Background()

	t.Run("Default args applied when client input is empty", func(t *testing.T) {
		capturedReq = SampleReq{}
		task, err := transport.Send(ctx, ta2a.Message{
			Role: "test-role",
			Parts: []ta2a.Part{
				{Type: ta2a.PartData, Data: map[string]any{"force": true}},
			},
		})
		if err != nil {
			t.Fatalf("unexpected Send error: %v", err)
		}
		if task.Status != ta2a.TaskStatusCompleted {
			t.Fatalf("expected completed task, got %s", task.Status)
		}

		if capturedReq.Profile != "default-profile" {
			t.Errorf("expected Profile=default-profile from DefaultArgs, got %q", capturedReq.Profile)
		}
		if capturedReq.Editor != "markdown" {
			t.Errorf("expected Editor=markdown from DefaultArgs, got %q", capturedReq.Editor)
		}
		if capturedReq.OutputFile != "HARD_OVERRIDE.md" {
			t.Errorf("expected OutputFile=HARD_OVERRIDE.md from Args, got %q", capturedReq.OutputFile)
		}
		if !capturedReq.Force {
			t.Errorf("expected Force=true from part data, got %v", capturedReq.Force)
		}
	})

	t.Run("Message Part Data overrides DefaultArgs", func(t *testing.T) {
		capturedReq = SampleReq{}
		_, err := transport.Send(ctx, ta2a.Message{
			Role: "test-role",
			Parts: []ta2a.Part{
				{
					Type: ta2a.PartData,
					Data: map[string]any{
						"editor":    "claude-xml",
						"max_lines": 500,
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected Send error: %v", err)
		}

		if capturedReq.Editor != "claude-xml" {
			t.Errorf("expected Editor=claude-xml to override default, got %q", capturedReq.Editor)
		}
		if capturedReq.MaxLines != 500 {
			t.Errorf("expected MaxLines=500 to override default, got %d", capturedReq.MaxLines)
		}
		if capturedReq.Profile != "default-profile" {
			t.Errorf("expected Profile=default-profile from defaults, got %q", capturedReq.Profile)
		}
		if capturedReq.OutputFile != "HARD_OVERRIDE.md" {
			t.Errorf("expected OutputFile=HARD_OVERRIDE.md, got %q", capturedReq.OutputFile)
		}
	})

	t.Run("Explicit text overrides candidate field", func(t *testing.T) {
		capturedReq = SampleReq{}
		_, err := transport.Send(ctx, ta2a.Message{
			Role: "test-role",
			Text: "custom-arch-profile",
		})
		if err != nil {
			t.Fatalf("unexpected Send error: %v", err)
		}

		if capturedReq.Profile != "custom-arch-profile" {
			t.Errorf("expected text to populate Profile, got %q", capturedReq.Profile)
		}
		if capturedReq.OutputFile != "HARD_OVERRIDE.md" {
			t.Errorf("expected hard override to win over text, got %q", capturedReq.OutputFile)
		}
	})
}

func TestTA2A_HITL_CompleteLifecycle(t *testing.T) {
	t.Parallel()

	var executedWithReq SampleReq
	var executionCount atomic.Int32

	hitlAct := action.New("orders.hitl.process", func(ctx context.Context, req SampleReq) (*SampleRes, error) {
		executionCount.Add(1)
		executedWithReq = req
		return &SampleRes{
			Profile: req.Profile,
			Content: "PAYMENT PROCESSED FOR " + req.Profile,
			Summary: "Done: " + req.Profile,
		}, nil
	}).
		Route(
			ta2a.Role("hitl-worker").
				WithHumanInTheLoop(ta2a.HITLConfig{
					TriggerWords: []string{"root-delete", "all", "."},
					Prompt:       "Dangerous operation requested. Confirm execution?",
					Options:      []string{"approve", "reject"},
				}).
				WithDefaultArgs(SampleReq{
					Profile: "standard-scope",
				}),
		).
		Build()

	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{hitlAct})
	ctx := context.Background()

	t.Run("Safe non-trigger execution proceeds immediately", func(t *testing.T) {
		executionCount.Store(0)
		task, err := transport.Send(ctx, ta2a.Message{
			Role: "hitl-worker",
			Text: "safe-subsystem",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Status != ta2a.TaskStatusCompleted {
			t.Fatalf("expected completed status, got: %s", task.Status)
		}
		if executionCount.Load() != 1 {
			t.Fatalf("expected action to execute exactly 1 time, got %d", executionCount.Load())
		}
		if executedWithReq.Profile != "safe-subsystem" {
			t.Errorf("expected Profile=safe-subsystem, got %q", executedWithReq.Profile)
		}
	})

	t.Run("Trigger word halts and returns input-required form", func(t *testing.T) {
		executionCount.Store(0)
		contextID := "ctx-audit-999"

		task, err := transport.Send(ctx, ta2a.Message{
			Role:      "hitl-worker",
			Text:      "root-delete",
			ContextID: contextID,
			Parts: []ta2a.Part{
				{Type: ta2a.PartData, Data: map[string]any{"editor": "zed-editor"}},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if task.Status != ta2a.TaskStatusInputRequired {
			t.Fatalf("expected status %s, got %s", ta2a.TaskStatusInputRequired, task.Status)
		}
		if executionCount.Load() != 0 {
			t.Fatalf("action MUST NOT execute during HITL pause")
		}
		if len(task.Artifacts) != 1 || task.Artifacts[0].Name != "ApprovalForm" {
			t.Fatalf("expected ApprovalForm artifact, got: %+v", task.Artifacts)
		}

		if len(task.Transitions) < 2 {
			t.Fatalf("expected at least 2 transitions, got %d", len(task.Transitions))
		}
		if task.Transitions[len(task.Transitions)-1].To != ta2a.TaskStatusInputRequired {
			t.Errorf("expected final transition to input-required, got %s", task.Transitions[len(task.Transitions)-1].To)
		}

		approvedTask, err := transport.Send(ctx, ta2a.Message{
			Role:      "hitl-worker",
			Text:      "approve",
			ContextID: contextID,
		})
		if err != nil {
			t.Fatalf("approval resume failed: %v", err)
		}

		if approvedTask.Status != ta2a.TaskStatusCompleted {
			t.Fatalf("expected completed status after approval, got %s", approvedTask.Status)
		}
		if executionCount.Load() != 1 {
			t.Fatalf("action must execute exactly once upon approval")
		}

		if executedWithReq.Profile != "root-delete" {
			t.Errorf("original Profile=root-delete was lost, action received %q", executedWithReq.Profile)
		}
		if executedWithReq.Editor != "zed-editor" {
			t.Errorf("original part data Editor=zed-editor was lost, action received %q", executedWithReq.Editor)
		}

		_, err = transport.Send(ctx, ta2a.Message{
			Role:      "hitl-worker",
			Text:      "approve",
			ContextID: contextID,
		})
		if err == nil {
			t.Fatalf("expected KindConflict when approving already cleared context, got nil")
		}
		if xerr.KindFrom(err) != xerr.KindConflict {
			t.Errorf("expected KindConflict, got %s (err: %v)", xerr.KindFrom(err), err)
		}
	})

	t.Run("Trigger word halts and handles operator rejection", func(t *testing.T) {
		executionCount.Store(0)
		contextID := "ctx-reject-123"

		task, err := transport.Send(ctx, ta2a.Message{
			Role:      "hitl-worker",
			Text:      "all",
			ContextID: contextID,
		})
		if err != nil || task.Status != ta2a.TaskStatusInputRequired {
			t.Fatalf("expected input-required, got: %s (err=%v)", task.Status, err)
		}

		rejectedTask, err := transport.Send(ctx, ta2a.Message{
			Role:      "hitl-worker",
			Text:      "reject",
			ContextID: contextID,
		})
		if err != nil {
			t.Fatalf("unexpected error on reject: %v", err)
		}

		if rejectedTask.Status != ta2a.TaskStatusRejected {
			t.Fatalf("expected status %s, got %s", ta2a.TaskStatusRejected, rejectedTask.Status)
		}
		if executionCount.Load() != 0 {
			t.Fatalf("action MUST NOT execute when rejected")
		}

		lastTrans := rejectedTask.Transitions[len(rejectedTask.Transitions)-1]
		if lastTrans.To != ta2a.TaskStatusRejected {
			t.Errorf("expected transition to rejected, got %s", lastTrans.To)
		}
	})
}

func TestTA2A_ArtifactsAndSummaryTemplates(t *testing.T) {
	t.Parallel()

	act := action.New("code.pack", func(ctx context.Context, req SampleReq) (*SampleRes, error) {
		return &SampleRes{
			Profile:    req.Profile,
			Content:    "RAW_HUGE_CODEBASE_CONTENT_STRING",
			Summary:    "Short summary header",
			FileCount:  24,
			FuncCount:  142,
			TotalBytes: 8192,
		}, nil
	}).
		Route(
			ta2a.Role("architect").
				WithArgs(SampleReq{Profile: "arch"}).
				WithArtifact("signatures_${Profile}.md", "text/markdown").
				WithSummaryTemplate("AST Map: ${FUNC_COUNT} public funcs in ${FILE_COUNT} files (${TOTALBYTES} bytes)."),
		).
		Build()

	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{act})

	task, err := transport.Send(context.Background(), ta2a.Message{
		Role: "architect",
		Text: "ignored-due-to-withargs",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if len(task.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(task.Artifacts))
	}
	art := task.Artifacts[0]
	if art.Name != "signatures_arch.md" {
		t.Errorf("expected artifact name 'signatures_arch.md', got %q", art.Name)
	}
	if art.Data != "RAW_HUGE_CODEBASE_CONTENT_STRING" {
		t.Errorf("expected artifact data to contain raw content")
	}

	if strings.Contains(task.Text, "RAW_HUGE_CODEBASE_CONTENT_STRING") {
		t.Fatalf("task.Text duplicated the raw Content string when it should use template/summary")
	}

	expectedSummary := "AST Map: 142 public funcs in 24 files (8192 bytes)."
	if task.Text != expectedSummary {
		t.Errorf("summary template evaluation mismatch:\ngot:  %q\nwant: %q", task.Text, expectedSummary)
	}
}

func TestTA2A_MemoryManagement_TaskTTLEviction(t *testing.T) {
	t.Parallel()

	act := action.New("ping", func(ctx context.Context, _ any) (string, error) {
		return "pong", nil
	}).
		Route(ta2a.Role("pinger")).
		Build()

	shortTTL := 50 * time.Millisecond
	transport := ta2a.New(":0", nil, ta2a.WithTaskTTL(shortTTL))
	transport.Mount([]action.AnyAction{act})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	task, err := transport.Send(ctx, ta2a.Message{
		Role:      "pinger",
		Text:      "test",
		ContextID: "ctx-ephemeral-1",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Avoid shadow declaration of err
	if _, getErr := transport.Get(ctx, task.ID); getErr != nil {
		t.Fatalf("expected task %s to exist immediately, got error: %v", task.ID, getErr)
	}

	go func() { _, _ = transport.Do(ctx, nil) }()

	time.Sleep(150 * time.Millisecond)

	_, err = transport.Get(ctx, task.ID)
	if err == nil {
		t.Fatalf("expected task %s to be evicted after TTL %v, but it still exists", task.ID, shortTTL)
	}
	if xerr.KindFrom(err) != xerr.KindNotFound {
		t.Errorf("expected KindNotFound for evicted task, got: %s (err: %v)", xerr.KindFrom(err), err)
	}
}

func TestTA2A_Webhook_Delivery_WithHMACAndRetry(t *testing.T) {
	t.Parallel()

	var (
		mu                sync.Mutex
		attempts          int
		receivedSignature string
		receivedBody      []byte
		receivedTask      ta2a.Task
	)

	webhookSecret := "super-secure-secret-key-42"
	doneCh := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		attempts++
		currAttempt := attempts
		receivedSignature = r.Header.Get("X-A2A-Signature")
		receivedBody = append([]byte(nil), body...)
		_ = json.Unmarshal(body, &receivedTask)
		mu.Unlock()

		// Fail attempt 1, succeed on attempt 2
		if currAttempt == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		select {
		case doneCh <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	act := action.New("webhook.action", func(ctx context.Context, _ any) (string, error) {
		return "webhook-result", nil
	}).
		Route(ta2a.Role("webhook-role")).
		Build()

	transport := ta2a.New(":0", nil,
		ta2a.WithWebhookSecret(webhookSecret),
		ta2a.WithWebhookHTTPClient(ts.Client()),
	)
	transport.Mount([]action.AnyAction{act})

	task, err := transport.Send(context.Background(), ta2a.Message{
		Role:        "webhook-role",
		Text:        "ping",
		CallbackURL: ts.URL,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Wait for successful delivery
	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook delivery retry")
	}

	// Safely read captured state under lock
	mu.Lock()
	finalAttempts := attempts
	finalTask := receivedTask
	finalSig := receivedSignature
	finalBody := append([]byte(nil), receivedBody...)
	mu.Unlock()

	if finalAttempts != 2 {
		t.Fatalf("expected 2 webhook delivery attempts (1 retry), got %d", finalAttempts)
	}
	if finalTask.ID != task.ID {
		t.Errorf("webhook received task ID %q, want %q", finalTask.ID, task.ID)
	}

	// Verify HMAC-SHA256 signature against the exact received payload
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(finalBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if finalSig != expectedSig {
		t.Errorf("HMAC signature mismatch:\ngot:  %s\nwant: %s", finalSig, expectedSig)
	}
}

func TestTA2A_ActionErrorTaxonomy(t *testing.T) {
	t.Parallel()

	errAct := action.New("action.error", func(ctx context.Context, _ any) (string, error) {
		return "", xerr.Unauthorized("invalid credentials for service")
	}).
		Route(ta2a.Role("unauthorized-role")).
		Build()

	panicAct := action.New("action.panic", func(ctx context.Context, _ any) (string, error) {
		panic("fatal database driver crash")
	}).
		Route(ta2a.Role("crashing-role")).
		Build()

	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{errAct, panicAct})

	ctx := context.Background()

	t.Run("Propagates structured xerr error taxonomy", func(t *testing.T) {
		task, err := transport.Send(ctx, ta2a.Message{
			Role: "unauthorized-role",
			Text: "check",
		})
		if err == nil {
			t.Fatalf("expected error from Send, got nil")
		}

		if task.Status != ta2a.TaskStatusFailed {
			t.Errorf("expected TaskStatusFailed, got %s", task.Status)
		}
		if task.Error == nil || task.Error.Code != string(xerr.KindUnauthorized) {
			t.Errorf("expected TaskError.Code=%s, got %+v", xerr.KindUnauthorized, task.Error)
		}
		if !strings.Contains(task.Text, "invalid credentials") {
			t.Errorf("expected error message in task.Text, got: %q", task.Text)
		}

		var appErr *xerr.AppError
		if !errors.As(err, &appErr) || appErr.Kind != xerr.KindUnauthorized {
			t.Errorf("expected standard errors.As to unwrap *AppError with KindUnauthorized, got: %v", err)
		}
	})

	t.Run("Safely captures panic turning it into failed task", func(t *testing.T) {
		task, err := transport.Send(ctx, ta2a.Message{
			Role: "crashing-role",
			Text: "blowup",
		})
		if err == nil {
			t.Fatalf("expected error on panic, got nil")
		}
		if task.Status != ta2a.TaskStatusFailed {
			t.Errorf("expected TaskStatusFailed, got %s", task.Status)
		}
		if task.Error == nil {
			t.Errorf("expected populated Task.Error on action panic")
		}
	})

	t.Run("Unknown role returns not found error", func(t *testing.T) {
		_, err := transport.Send(ctx, ta2a.Message{
			Role: "non-existent-role",
			Text: "hello",
		})
		if err == nil {
			t.Fatalf("expected error for unknown role, got nil")
		}
		if xerr.KindFrom(err) != xerr.KindNotFound {
			t.Errorf("expected KindNotFound, got %s (err: %v)", xerr.KindFrom(err), err)
		}
	})
}

func TestTA2A_Task_Cancellation(t *testing.T) {
	t.Parallel()

	act := action.New("dummy", func(ctx context.Context, _ any) (string, error) {
		return "ok", nil
	}).
		Route(ta2a.Role("canceller")).
		Build()

	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{act})
	ctx := context.Background()

	task, err := transport.Send(ctx, ta2a.Message{
		Role: "canceller",
		Text: "start",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Avoid shadow declaration of err
	if cancelErr := transport.Cancel(ctx, task.ID); cancelErr != nil {
		t.Fatalf("Cancel failed: %v", cancelErr)
	}

	canceledTask, err := transport.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get task failed: %v", err)
	}

	if canceledTask.Status != ta2a.TaskStatusCanceled {
		t.Errorf("expected status %s, got %s", ta2a.TaskStatusCanceled, canceledTask.Status)
	}

	lastTrans := canceledTask.Transitions[len(canceledTask.Transitions)-1]
	if lastTrans.To != ta2a.TaskStatusCanceled {
		t.Errorf("expected last transition to be canceled, got %s", lastTrans.To)
	}
}
