package tmcp_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nexssp/testkit"
	"github.com/nexssp/transport/thttp"
	"github.com/nexssp/transportai/tmcp"
)

func TestTMCP_SSE_SessionLifecycle(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "sse", addAction())
	suite := testkit.NewWithHandler(t, srv.Handler())

	stream := suite.ListenSSE("/mcp/sse")
	defer stream.Close()

	endpoint := stream.Endpoint(t, 2*time.Second)

	suite.POST(endpoint, map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "math.add",
			"arguments": map[string]any{
				"a": 20,
				"b": 22,
			},
		},
	}).
		Do().
		ExpectStatus(http.StatusAccepted)

	evt := stream.WaitFor(t, "message", 2*time.Second)

	var rpcResp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(evt.Data), &rpcResp); err != nil {
		t.Fatalf("invalid JSON-RPC envelope: %v\nRaw: %s", err, evt.Data)
	}

	if len(rpcResp.Result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(rpcResp.Result.Content))
	}

	var sum AddRes
	if err := json.Unmarshal([]byte(rpcResp.Result.Content[0].Text), &sum); err != nil {
		t.Fatalf("invalid tool payload: %v\nText: %s", err, rpcResp.Result.Content[0].Text)
	}

	if sum.Sum != 42 {
		t.Fatalf("expected sum 42, got %d", sum.Sum)
	}
}

func TestHTTP_SyncFallback(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "sync", addAction())
	suite := testkit.NewWithHandler(t, srv.Handler())

	var resp tmcp.Response

	suite.POST("/mcp/message", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}).
		Do().
		ExpectOK().
		Into(&resp)

	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
}

func TestHTTPRoutes(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("routes", "1.0.0")
	routes := srv.HTTPRoutes()

	if len(routes) != 2 {
		t.Fatalf("expected 2 HTTP routes, got %d", len(routes))
	}

	for _, route := range routes {
		bindings := route.GetBindings()
		if len(bindings) != 1 {
			t.Fatalf("expected 1 binding, got %d", len(bindings))
		}

		if _, ok := bindings[0].(thttp.RawHTTPHandler); !ok {
			t.Fatalf("expected thttp.RawHTTPHandler, got %T", bindings[0])
		}
	}
}
