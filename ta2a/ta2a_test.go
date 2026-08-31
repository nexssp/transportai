package ta2a_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transportai/ta2a"
)

type mockAgent struct {
	mu    sync.Mutex
	tasks map[string]ta2a.Task
}

func (a *mockAgent) Card(context.Context) (ta2a.AgentCard, error) {
	return ta2a.AgentCard{Name: "support-agent", Version: "1.0.0"}, nil
}

func (a *mockAgent) Send(_ context.Context, message ta2a.Message) (ta2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tasks == nil {
		a.tasks = make(map[string]ta2a.Task)
	}
	task := ta2a.Task{ID: "task-100", ContextID: message.ContextID, State: "completed", Text: "Processed"}
	a.tasks[task.ID] = task
	return task, nil
}

func (a *mockAgent) Get(_ context.Context, id string) (ta2a.Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	task, ok := a.tasks[id]
	if !ok {
		return ta2a.Task{}, xerr.NotFound(fmt.Sprintf("task %s not found", id))
	}
	return task, nil
}

func (a *mockAgent) Cancel(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	task, ok := a.tasks[id]
	if !ok {
		return xerr.NotFound(fmt.Sprintf("task %s not found", id))
	}
	task.State = "canceled"
	a.tasks[id] = task
	return nil
}

func TestTA2A_Lifecycle(t *testing.T) {
	t.Parallel()

	agent := &mockAgent{}
	srv := ta2a.New(":0", agent)

	// 1. Discovery Agent Card
	cardRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cardRec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if cardRec.Code != http.StatusOK || !strings.Contains(cardRec.Body.String(), "support-agent") {
		t.Fatalf("card status=%d body=%s", cardRec.Code, cardRec.Body.String())
	}

	// 2. Send Message
	sendRec := httptest.NewRecorder()
	sendReq := httptest.NewRequest(http.MethodPost, "/message/send", strings.NewReader(`{"message":{"role":"user","text":"Hello A2A"}}`))
	srv.Handler().ServeHTTP(sendRec, sendReq)

	if sendRec.Code != http.StatusOK || !strings.Contains(sendRec.Body.String(), "task-100") {
		t.Fatalf("send status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
}

func TestTA2A_AsAction(t *testing.T) {
	t.Parallel()

	srv := ta2a.New(":8080", &mockAgent{})
	act := srv.AsAction().Build()

	if act.Describe().Name != "transport.a2a.:8080" {
		t.Fatalf("unexpected action name: %s", act.Describe().Name)
	}
}

func TestTA2A_Cancel_NotFoundMapsToProperStatus(t *testing.T) {
	t.Parallel()

	agent := &mockAgent{} // Cancel currently always returns nil; see note below
	srv := ta2a.New(":0", agent)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tasks/cancel", strings.NewReader(`{"id":"does-not-exist"}`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown task, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTA2A_Send_OversizedBodyReturns413(t *testing.T) {
	t.Parallel()

	agent := &mockAgent{}
	srv := ta2a.New(":0", agent)

	// Build a message text large enough to exceed the 1 MiB default maxBodyBytes.
	oversized := strings.Repeat("a", 2<<20) // 2 MiB
	body := fmt.Sprintf(`{"message":{"role":"user","text":"%s"}}`, oversized)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTA2A_Send_MalformedSmallBodyReturns400(t *testing.T) {
	t.Parallel()

	agent := &mockAgent{}
	srv := ta2a.New(":0", agent)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", strings.NewReader(`{not valid json`))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d body=%s", rec.Code, rec.Body.String())
	}
}
