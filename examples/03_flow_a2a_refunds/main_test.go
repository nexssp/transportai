package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/flow/native"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transportai/nexssflow"
	"github.com/nexssp/transportai/ta2a"
)

func TestFlowRefundApprovalOverA2A(t *testing.T) {
	bundle, err := nexssflow.NewBundle(nexssflow.Config{A2AAddr: ":0"})
	if err != nil {
		t.Fatalf("build transport Flow bundle: %v", err)
	}
	cfg, err := runner.BuildConfig(append(native.Bundles(), bundle))
	if err != nil {
		t.Fatalf("build Flow config: %v", err)
	}
	sourceBytes, err := flowSource.ReadFile("service.nflow")
	if err != nil {
		t.Fatalf("read embedded Flow source: %v", err)
	}
	compiled, err := runner.Compile(t.Context(), cfg, string(sourceBytes), "service.nflow")
	if err != nil {
		t.Fatalf("compile Flow program: %v", err)
	}
	endpoint, ok := compiled.Resolver.Action("pipeline.refund_review")
	if !ok {
		t.Fatal("compiled resolver does not contain pipeline.refund_review")
	}

	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{endpoint})
	server := httptest.NewServer(transport.Handler())
	defer server.Close()

	request := ta2a.Message{
		Role:      "billing.refund",
		ContextID: "refund-test-1042",
		Text:      "refund",
		Parts: []ta2a.Part{{
			Type: ta2a.PartData,
			Data: map[string]any{
				"ticket_id":    "SUP-1042",
				"customer_id":  "cust-88",
				"amount_cents": 4599,
				"reason":       "Duplicate annual renewal",
			},
		}},
	}
	paused := postMessage(t, server.URL, request)
	if paused.Status != ta2a.TaskStatusInputRequired {
		t.Fatalf("first task status = %q, want %q (task: %+v)", paused.Status, ta2a.TaskStatusInputRequired, paused)
	}
	if len(paused.Artifacts) != 1 || paused.Artifacts[0].Name != "ApprovalForm" {
		t.Fatalf("approval artifact = %+v, want ApprovalForm", paused.Artifacts)
	}

	request.Text = "approve"
	approved := postMessage(t, server.URL, request)
	if approved.Status != ta2a.TaskStatusCompleted {
		t.Fatalf("approved task status = %q, want %q (task: %+v)", approved.Status, ta2a.TaskStatusCompleted, approved)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(approved.Text), &result); err != nil {
		t.Fatalf("decode completed task body %q: %v", approved.Text, err)
	}
	if result["ticket_id"] != "SUP-1042" || result["disposition"] != "approved_for_manual_processing" {
		t.Fatalf("completed Flow result = %#v", result)
	}

	invalidRequests := []struct {
		name      string
		contextID string
		data      map[string]any
		wantError string
	}{
		{
			name:      "missing required ticket_id",
			contextID: "refund-missing-ticket-id",
			data: map[string]any{
				"customer_id":  "cust-88",
				"amount_cents": 4599,
				"reason":       "Duplicate annual renewal",
			},
			wantError: `field "ticket_id" is required`,
		},
		{
			name:      "wrong amount_cents datatype",
			contextID: "refund-string-amount",
			data: map[string]any{
				"ticket_id":    "SUP-1043",
				"customer_id":  "cust-88",
				"amount_cents": "4599",
				"reason":       "Duplicate annual renewal",
			},
			wantError: `field "amount_cents" must be a number (got string)`,
		},
	}
	for _, tc := range invalidRequests {
		t.Run(tc.name, func(t *testing.T) {
			invalid := ta2a.Message{
				Role:      "billing.refund",
				ContextID: tc.contextID,
				Text:      "refund",
				Parts: []ta2a.Part{{
					Type: ta2a.PartData,
					Data: tc.data,
				}},
			}
			paused, err := transport.Send(t.Context(), invalid)
			if err != nil || paused.Status != ta2a.TaskStatusInputRequired {
				t.Fatalf("invalid request approval status = %+v, error = %v", paused, err)
			}

			invalid.Text = "approve"
			failed, err := transport.Send(t.Context(), invalid)
			if err == nil || failed.Status != ta2a.TaskStatusFailed {
				t.Fatalf("approved invalid request = %+v, error = %v; want validation failure", failed, err)
			}
			if failed.Error == nil || !strings.Contains(failed.Error.Message, tc.wantError) {
				t.Fatalf("validation error = %+v, want %q", failed.Error, tc.wantError)
			}
		})
	}
}

func postMessage(t *testing.T, baseURL string, message ta2a.Message) ta2a.Task {
	t.Helper()
	body, err := json.Marshal(struct {
		Message ta2a.Message `json:"message"`
	}{Message: message})
	if err != nil {
		t.Fatalf("encode A2A message: %v", err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/message/send", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create A2A request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send A2A request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			t.Fatalf("read A2A error response: %v", readErr)
		}
		t.Fatalf("A2A response status = %d, body: %s", response.StatusCode, payload)
	}
	var task ta2a.Task
	if err := json.NewDecoder(response.Body).Decode(&task); err != nil {
		t.Fatalf("decode A2A task: %v", err)
	}
	return task
}
