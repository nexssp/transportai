package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/flow/native"
	"github.com/nexssp/flow/runner"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transportai/ta2a"
	a2aflow "github.com/nexssp/transportai/ta2a/nexssflow"
	"github.com/nexssp/transportai/tmcp"
	mcpflow "github.com/nexssp/transportai/tmcp/nexssflow"
)

//go:embed service.nflow
var flowSource embed.FS

func TestFlowPipelineIsPublishedAndCalledAsMCPTool(t *testing.T) {
	server, ctx := compiledToolServer(t)
	request := strings.Join([]string{
		rpcRequest(t, 1, "initialize", map[string]any{}),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		rpcRequest(t, 2, "tools/list", map[string]any{}),
		rpcRequest(t, 3, "tools/call", map[string]any{
			"name": "support.ticket_triage",
			"arguments": map[string]any{
				"ticket_id": "SUP-2048",
				"category":  "billing",
				"urgency":   "high",
				"summary":   "Duplicate annual charge",
			},
		}),
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := server.Serve(ctx, strings.NewReader(request), &output); err != nil {
		t.Fatalf("serve MCP requests: %v", err)
	}

	decoder := json.NewDecoder(&output)
	var initialize struct {
		ID int `json:"id"`
	}
	if err := decoder.Decode(&initialize); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initialize.ID != 1 {
		t.Fatalf("initialize response ID = %d, want 1", initialize.ID)
	}

	var list struct {
		ID     int `json:"id"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := decoder.Decode(&list); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	if list.ID != 2 || len(list.Result.Tools) != 1 || list.Result.Tools[0].Name != "support.ticket_triage" {
		t.Fatalf("published tools = %+v, want support.ticket_triage", list)
	}

	var call struct {
		ID     int `json:"id"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := decoder.Decode(&call); err != nil {
		t.Fatalf("decode tools/call response: %v", err)
	}
	if call.ID != 3 || call.Error != nil || call.Result.IsError || len(call.Result.Content) != 1 {
		t.Fatalf("tool call response = %+v", call)
	}

	var result struct {
		TicketID string `json:"ticket_id"`
		Priority string `json:"priority"`
		Queue    string `json:"queue"`
		SLA      int    `json:"response_sla_minutes"`
	}
	if err := json.Unmarshal([]byte(call.Result.Content[0].Text), &result); err != nil {
		t.Fatalf("decode triage output %q: %v", call.Result.Content[0].Text, err)
	}
	if result.TicketID != "SUP-2048" || result.Priority != "P2" || result.Queue != "billing-operations" || result.SLA != 240 {
		t.Fatalf("triage result = %+v", result)
	}
}

func TestFlowPipelinePreservesRoutingRules(t *testing.T) {
	server, ctx := compiledToolServer(t)
	tests := []struct {
		name, ticketID, category, urgency string
		priority, queue                   string
		sla                               int
		nextAction                        string
	}{
		{"critical overrides billing", "SUP-3001", "billing", "critical", "P1", "on-call", 15, "page_on_call"},
		{"access", "SUP-3002", "access", "normal", "P2", "identity-operations", 120, "verify_account_access"},
		{"general fallback", "SUP-3003", "product", "low", "P3", "customer-support", 480, "investigate_and_reply"},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := rpcRequest(t, i+1, "tools/call", map[string]any{
				"name": "support.ticket_triage",
				"arguments": map[string]any{
					"ticket_id": tc.ticketID,
					"category":  tc.category,
					"urgency":   tc.urgency,
					"summary":   "Routing regression test",
				},
			})
			var output bytes.Buffer
			if err := server.Serve(ctx, strings.NewReader(request), &output); err != nil {
				t.Fatalf("serve MCP request: %v", err)
			}

			var response struct {
				Result struct {
					IsError bool `json:"isError"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
				t.Fatalf("decode tool response: %v", err)
			}
			if response.Result.IsError || len(response.Result.Content) != 1 {
				t.Fatalf("tool response = %+v", response)
			}

			var result struct {
				TicketID   string `json:"ticket_id"`
				Priority   string `json:"priority"`
				Queue      string `json:"queue"`
				SLA        int    `json:"response_sla_minutes"`
				NextAction string `json:"next_action"`
			}
			if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &result); err != nil {
				t.Fatalf("decode triage result %q: %v", response.Result.Content[0].Text, err)
			}
			if result.TicketID != tc.ticketID || result.Priority != tc.priority || result.Queue != tc.queue || result.SLA != tc.sla || result.NextAction != tc.nextAction {
				t.Fatalf("triage result = %+v", result)
			}
		})
	}
}

func TestFlowMCPToolRejectsUnsupportedUrgency(t *testing.T) {
	server, ctx := compiledToolServer(t)
	request := rpcRequest(t, 1, "tools/call", map[string]any{
		"name": "support.ticket_triage",
		"arguments": map[string]any{
			"ticket_id": "SUP-2049",
			"category":  "billing",
			"urgency":   "urgent",
			"summary":   "Test invalid input",
		},
	})
	var output bytes.Buffer
	if err := server.Serve(ctx, strings.NewReader(request), &output); err != nil {
		t.Fatalf("serve MCP request: %v", err)
	}

	var response struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatalf("decode tool error response: %v", err)
	}
	if !response.Result.IsError || len(response.Result.Content) == 0 || !strings.Contains(response.Result.Content[0].Text, "urgency must be") {
		t.Fatalf("invalid urgency response = %+v", response)
	}
}

func TestFlowMCPToolRejectsMissingAndWrongTypedSchemaFields(t *testing.T) {
	server, ctx := compiledToolServer(t)
	tests := []struct {
		name      string
		arguments map[string]any
		wantError string
	}{
		{
			name: "missing required ticket_id",
			arguments: map[string]any{
				"category": "billing",
				"urgency":  "high",
				"summary":  "Missing identifier",
			},
			wantError: `field "ticket_id" is required`,
		},
		{
			name: "wrong ticket_id datatype",
			arguments: map[string]any{
				"ticket_id": 2048,
				"category":  "billing",
				"urgency":   "high",
				"summary":   "Numeric identifier",
			},
			wantError: `field "ticket_id" must be a string (got number)`,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := rpcRequest(t, i+1, "tools/call", map[string]any{
				"name":      "support.ticket_triage",
				"arguments": tc.arguments,
			})
			var output bytes.Buffer
			if err := server.Serve(ctx, strings.NewReader(request), &output); err != nil {
				t.Fatalf("serve MCP request: %v", err)
			}

			var response struct {
				Result struct {
					IsError bool `json:"isError"`
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
				t.Fatalf("decode tool error response: %v", err)
			}
			if !response.Result.IsError || len(response.Result.Content) == 0 || !strings.Contains(response.Result.Content[0].Text, tc.wantError) {
				t.Fatalf("schema validation response = %+v, want error containing %q", response, tc.wantError)
			}
		})
	}
}

func compiledToolServer(t *testing.T) (*tmcp.Transport, context.Context) {
	t.Helper()
	server := tmcp.New("support-ticket-triage", "1.0.0")
	cfg, err := runner.BuildConfig(append(native.Bundles(), coreBundle(server)))
	if err != nil {
		t.Fatalf("build Flow config: %v", err)
	}
	source, err := flowSource.ReadFile("service.nflow")
	if err != nil {
		t.Fatalf("read embedded Flow source: %v", err)
	}
	compiled, err := runner.Compile(t.Context(), cfg, string(source), "service.nflow")
	if err != nil {
		t.Fatalf("compile Flow source: %v", err)
	}
	pipeline, ok := compiled.Resolver.Action("pipeline.triage")
	if !ok {
		t.Fatal("compiled resolver does not contain pipeline.triage")
	}
	server.Mount([]action.AnyAction{pipeline})
	return server, contracts.WithActionResolver(t.Context(), compiled.Resolver)
}

func coreBundle(server *tmcp.Transport) compiler.Bundle {
	return compiler.Bundle{
		ID: "tmcp-example",
		Libraries: []action.Library{
			mcpflow.Library(server),
			a2aflow.Library(ta2a.New(":0", nil)),
		},
		Modifiers: append(mcpflow.Modifiers(), a2aflow.Modifiers()...),
	}
}

func rpcRequest(t *testing.T, id int, method string, params any) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		t.Fatalf("marshal JSON-RPC request: %v", err)
	}
	return string(payload)
}
