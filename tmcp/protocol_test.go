package tmcp_test

import (
	"testing"

	"github.com/nexssp/transportai/tmcp"
)

func TestInitialize(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "init-mcp")
	client := newTestClient(t, srv)

	resp := client.Call("initialize", map[string]any{}, 1)

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools map[string]bool `json:"tools"`
		} `json:"capabilities"`
	}

	resp.BindResult(t, &result)

	if result.ProtocolVersion != tmcp.ProtocolVersion {
		t.Fatalf("unexpected protocol version: %s", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "init-mcp" || result.ServerInfo.Version != "1.0.0" {
		t.Fatalf("unexpected server info: %+v", result.ServerInfo)
	}
	if _, ok := result.Capabilities.Tools["listChanged"]; !ok {
		t.Fatalf("expected tools capability: %+v", result.Capabilities)
	}
}

func TestPing(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "ping"))

	resp := client.Call("ping", nil, 1)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestNotification_NoResponse(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "notify"))

	if err := client.Notify("notifications/initialized", nil); err != nil {
		t.Fatalf("notify failed: %v", err)
	}

	// Ensure the connection is still usable.
	resp := client.Call("ping", nil, 2)
	if resp.Error != nil {
		t.Fatalf("unexpected error after notification: %+v", resp.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "unknown"))

	resp := client.Call("unknown/method", nil, 1)
	if resp.Error == nil || resp.Error.Code != tmcp.CodeMethodNotFound {
		t.Fatalf("expected %d, got %+v", tmcp.CodeMethodNotFound, resp.Error)
	}
}

func TestInvalidJSONRPCVersion(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "invalid"))

	resp := client.CallRaw(`{"jsonrpc":"1.0","id":1,"method":"ping"}`)
	if resp.Error == nil || resp.Error.Code != tmcp.CodeInvalidRequest {
		t.Fatalf("expected %d, got %+v", tmcp.CodeInvalidRequest, resp.Error)
	}
}

func TestLoggingSetLevel(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "logging"))

	resp := client.Call("logging/setLevel", map[string]any{"level": "debug"}, 1)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestParseError(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "parse"))

	resp := client.CallRaw(`{invalid json payload`)
	if resp.Error == nil || resp.Error.Code != tmcp.CodeParseError {
		t.Fatalf("expected %d, got %+v", tmcp.CodeParseError, resp.Error)
	}
}
