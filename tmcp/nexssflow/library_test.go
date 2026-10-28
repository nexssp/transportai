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

	"github.com/nexssp/transportai/tmcp"
)

type testResolver struct {
	actions map[string]action.AnyAction
}

func (r testResolver) Action(name string) (action.AnyAction, bool) {
	act, ok := r.actions[name]
	return act, ok
}

func TestFlowListenerServesMCPOverHTTP(t *testing.T) {
	t.Setenv(tmcp.HTTPBearerTokenEnv, "test-only-token-not-for-use")

	listenConfig := net.ListenConfig{}
	reserved, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test address: %v", err)
	}
	addr := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	tool := action.New("pipeline.greet", func(_ context.Context, input map[string]any) (string, error) {
		name, _ := input["name"].(string)
		return "Hello " + name, nil
	}).Route(tmcp.Tool("greeting.say")).Build()
	bundle, err := NewBundle(Config{Name: "flow-http-test", Addr: addr})
	if err != nil {
		t.Fatalf("build MCP Flow bundle: %v", err)
	}
	if len(bundle.Libraries) != 1 || len(bundle.Libraries[0].Actions) != 1 {
		t.Fatalf("unexpected MCP Flow libraries: %+v", bundle.Libraries)
	}
	listener := bundle.Libraries[0].Actions[0]

	resolver := testResolver{actions: map[string]action.AnyAction{"pipeline.greet": tool}}
	ready := contracts.NewReadySignal()
	ctx, cancel := context.WithCancel(contracts.WithServiceReady(contracts.WithActionResolver(context.Background(), resolver), ready))
	defer cancel()
	listenerErr := make(chan error, 1)
	go func() {
		_, err := listener.DoAny(ctx, map[string]any{"endpoints": []string{"pipeline.greet"}})
		listenerErr <- err
	}()
	select {
	case <-ready.Done():
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("MCP Flow listener did not signal readiness after binding")
	}

	client := &http.Client{Timeout: time.Second}
	endpoint := "http://" + addr + "/mcp/message"
	deadline := time.Now().Add(3 * time.Second)
	var response struct {
		JSONRPC string `json:"jsonrpc"`
		Result  struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *tmcp.RPCError `json:"error"`
	}
	for time.Now().Before(deadline) {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greeting.say","arguments":{"name":"Julia"}}}`
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
		if reqErr != nil {
			t.Fatalf("build MCP request: %v", reqErr)
		}
		req.Header.Set("Authorization", "Bearer test-only-token-not-for-use")
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			if resp.StatusCode == http.StatusOK {
				if decodeErr := json.NewDecoder(resp.Body).Decode(&response); decodeErr != nil {
					_ = resp.Body.Close()
					t.Fatalf("decode MCP response: %v", decodeErr)
				}
				_ = resp.Body.Close()
				break
			}
			_ = resp.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if response.JSONRPC != "2.0" || response.Error != nil || len(response.Result.Content) != 1 || response.Result.Content[0].Text != "Hello Julia" {
		t.Fatalf("unexpected MCP tool response from %s: %+v", endpoint, response)
	}

	cancel()
	select {
	case err := <-listenerErr:
		if err != nil {
			t.Fatalf("tmcp.listen returned an error after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tmcp.listen did not stop after context cancellation")
	}
}

func TestFlowListenerDoesNotSignalReadyWhenMCPBindFails(t *testing.T) {
	t.Setenv(tmcp.HTTPBearerTokenEnv, "test-only-token-not-for-use")
	listenConfig := net.ListenConfig{}
	reserved, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test address: %v", err)
	}
	defer reserved.Close()

	tool := action.New("pipeline.greet", func(_ context.Context, input map[string]any) (string, error) {
		name, _ := input["name"].(string)
		return "Hello " + name, nil
	}).Route(tmcp.Tool("greeting.say")).Build()
	bundle, err := NewBundle(Config{Name: "flow-http-test", Addr: reserved.Addr().String()})
	if err != nil {
		t.Fatalf("build MCP Flow bundle: %v", err)
	}
	listener := bundle.Libraries[0].Actions[0]
	ready := contracts.NewReadySignal()
	ctx := contracts.WithServiceReady(contracts.WithActionResolver(context.Background(), testResolver{
		actions: map[string]action.AnyAction{"pipeline.greet": tool},
	}), ready)

	_, err = listener.DoAny(ctx, map[string]any{"endpoints": []string{"pipeline.greet"}})
	if err == nil {
		t.Fatal("expected MCP bind failure")
	}
	select {
	case <-ready.Done():
		t.Fatal("Flow readiness was signaled after MCP bind failed")
	default:
	}
}
