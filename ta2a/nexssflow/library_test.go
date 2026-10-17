package nexssflow

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transportai/ta2a"
)

type testResolver struct {
	actions map[string]action.AnyAction
}

func (r testResolver) Action(name string) (action.AnyAction, bool) {
	act, ok := r.actions[name]
	return act, ok
}

func TestFlowListenerServesA2AOverHTTP(t *testing.T) {
	t.Setenv(ta2a.HTTPBearerTokenEnv, "test-only-not-secret")

	listenConfig := net.ListenConfig{}
	reserved, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test address: %v", err)
	}
	addr := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	greeting := action.New("pipeline.greet", func(_ context.Context, msg ta2a.Message) (string, error) {
		return "Hello " + msg.Text, nil
	}).Route(ta2a.Role("greeting")).Build()
	bundle, err := NewBundle(Config{
		Addr:        addr,
		Name:        "flow-http-test",
		Version:     "2.0.0",
		Description: "A Flow agent used by the HTTP integration test",
	})
	if err != nil {
		t.Fatalf("build A2A Flow bundle: %v", err)
	}
	if len(bundle.Libraries) != 1 || len(bundle.Libraries[0].Actions) != 1 {
		t.Fatalf("unexpected A2A Flow libraries: %+v", bundle.Libraries)
	}
	listener := bundle.Libraries[0].Actions[0]

	resolver := testResolver{actions: map[string]action.AnyAction{"pipeline.greet": greeting}}
	ctx, cancel := context.WithCancel(contracts.WithActionResolver(context.Background(), resolver))
	defer cancel()
	listenerErr := make(chan error, 1)
	go func() {
		_, err := listener.DoAny(ctx, map[string]any{"endpoints": []string{"pipeline.greet"}})
		listenerErr <- err
	}()

	client := &http.Client{Timeout: time.Second}
	baseURL := "http://" + addr
	deadline := time.Now().Add(3 * time.Second)
	var card ta2a.AgentCard
	var lastErr error
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/.well-known/agent-card.json", http.NoBody)
		if reqErr != nil {
			t.Fatalf("build agent-card request: %v", reqErr)
		}
		req.Header.Set("Authorization", "Bearer test-only-not-secret")
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			if resp.StatusCode == http.StatusOK {
				decodeErr := json.NewDecoder(resp.Body).Decode(&card)
				_ = resp.Body.Close()
				if decodeErr != nil {
					t.Fatalf("decode agent card: %v", decodeErr)
				}
				break
			}
			lastErr = resp.Body.Close()
		} else {
			lastErr = requestErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	if card.Name != "flow-http-test" || card.Version != "2.0.0" || card.Description != "A Flow agent used by the HTTP integration test" {
		t.Fatalf("agent card did not expose Flow configuration (last request error: %v): %+v", lastErr, card)
	}
	if _, ok := card.Roles["greeting"]; !ok {
		t.Fatalf("agent card did not expose the mounted Flow role: %+v", card.Roles)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/message/send", strings.NewReader(`{"message":{"role":"greeting","text":"Julia"}}`))
	if err != nil {
		t.Fatalf("build A2A message request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-only-not-secret")
	resp, err := client.Do(request)
	if err != nil {
		t.Fatalf("send A2A message over HTTP: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("A2A message returned HTTP %d", resp.StatusCode)
	}
	var task ta2a.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatalf("decode A2A task response: %v", err)
	}
	if task.Status != ta2a.TaskStatusCompleted || task.Text != "Hello Julia" {
		t.Fatalf("unexpected A2A task result: %+v", task)
	}

	cancel()
	select {
	case err := <-listenerErr:
		if err != nil {
			t.Fatalf("ta2a.listen returned an error after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ta2a.listen did not stop after context cancellation")
	}
}
