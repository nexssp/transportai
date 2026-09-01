package tmcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transportai/tmcp"
)

func TestToolsList_Empty(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "empty")
	client := newTestClient(t, srv)

	resp := client.Call("tools/list", nil, 1)

	var result struct {
		Tools []any `json:"tools"`
	}

	resp.BindResult(t, &result)

	if result.Tools == nil {
		t.Fatal("expected tools array to be non-nil")
	}
	if len(result.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(result.Tools))
	}
}

func TestToolsList_WithAction(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "tools", addAction())
	client := newTestClient(t, srv)

	resp := client.Call("tools/list", nil, 1)

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}

	resp.BindResult(t, &result)

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "math.add" {
		t.Fatalf("unexpected tool name: %s", result.Tools[0].Name)
	}
	if result.Tools[0].Description != "Adds two integers" {
		t.Fatalf("unexpected description: %s", result.Tools[0].Description)
	}
}

func TestToolsCall_Success(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "tools", addAction())
	client := newTestClient(t, srv)

	resp := client.Call("tools/call", map[string]any{
		"name":      "math.add",
		"arguments": map[string]any{"a": 10, "b": 32},
	}, 1)

	text := toolText(t, resp)

	var sum AddRes
	if err := json.Unmarshal([]byte(text), &sum); err != nil {
		t.Fatalf("invalid tool output JSON: %v\ntext: %s", err, text)
	}
	if sum.Sum != 42 {
		t.Fatalf("expected sum 42, got %d", sum.Sum)
	}
}

func TestToolsCall_MissingTool(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "tools")
	client := newTestClient(t, srv)

	resp := client.Call("tools/call", map[string]any{
		"name": "missing.tool",
	}, 1)

	if resp.Error == nil || resp.Error.Code != tmcp.CodeMethodNotFound {
		t.Fatalf("expected method not found, got %+v", resp.Error)
	}
}

func TestToolsCall_ActionErrorIsError(t *testing.T) {
	t.Parallel()

	failing := action.New("db.query", func(_ context.Context, _ struct{}) (string, error) {
		return "", xerr.NotFound("record not found")
	}).Build()

	srv := newTestServer(t, "error", failing)
	client := newTestClient(t, srv)

	resp := client.Call("tools/call", map[string]any{
		"name":      "db.query",
		"arguments": map[string]any{},
	}, 1)

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}

	resp.BindResult(t, &result)

	if !result.IsError {
		t.Fatal("expected isError to be true")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "record not found") {
		t.Fatalf("unexpected error content: %+v", result.Content)
	}
}

func TestToolsCall_PanicIsError(t *testing.T) {
	t.Parallel()

	panicAct := action.New("panic.tool", func(_ context.Context, _ struct{}) (string, error) {
		panic("boom")
	}).Build()

	srv := newTestServer(t, "panic", panicAct)
	client := newTestClient(t, srv)

	resp := client.Call("tools/call", map[string]any{
		"name":      "panic.tool",
		"arguments": map[string]any{},
	}, 1)

	var result struct {
		IsError bool `json:"isError"`
	}

	resp.BindResult(t, &result)

	if !result.IsError {
		t.Fatal("expected panic to become isError tool result")
	}
}

func TestToolsCall_TextMarshaler(t *testing.T) {
	t.Parallel()

	docAct := action.New("doc.get", func(_ context.Context, _ struct{}) (TextDocRes, error) {
		return TextDocRes{Markdown: "# Markdown"}, nil
	}).Build()

	srv := newTestServer(t, "doc", docAct)
	client := newTestClient(t, srv)

	resp := client.Call("tools/call", map[string]any{
		"name":      "doc.get",
		"arguments": map[string]any{},
	}, 1)

	if text := toolText(t, resp); text != "# Markdown" {
		t.Fatalf("expected markdown output, got %q", text)
	}
}
