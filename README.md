# nexss-transportai

AI-specific protocol transports for `nexss-kernel` actions.

Status: Production-ready A2A-style transport for Nexssp actions and DAG workflows.
Not yet certified against the official A2A protocol specification.

```text
                  ┌─────────────────────────────────┐
                  │      nexss-kernel actions       │
                  └────────────────┬────────────────┘
                                   │
         ┌─────────────────────────┴─────────────────────────┐
         │                                                   │
         ▼                                                   ▼
   tmcp (Model Context Protocol)             ta2a (Agent2Agent Protocol)
   * Claude Desktop / Cursor / Windsurf       * Agent Cards & Discovery
   * stdio & SSE/HTTP JSON-RPC                * Structured Multi-part Messages
   * Actions as MCP Tools & Docs              * HITL & Task Resumption
   * Prompt Templates & Completions           * SSE Streaming & Webhooks
```

## Protocol Status

These transports provide lightweight, kernel-native adapters for connecting AI agents and frameworks to `nexss-kernel` actions and DAG pipelines.

| Package | Supported Features |
| :--- | :--- |
| **`ta2a`** | • Agent Card Discovery (`/.well-known/agent-card.json`)<br>• Structured Messages (`text`, `file`, `data` parts)<br>• Full Task State Machine (`working`, `input-required`, `completed`, `canceled`, `failed`)<br>• Human-in-the-Loop (HITL) task continuation on `contextId`<br>• Server-Sent Events (SSE) Streaming (`POST /message/stream`)<br>• Asynchronous Webhook callbacks<br>• `ta2a.Client` for calling remote A2A agents directly inside DAG workflows |
| **`tmcp`** | • JSON-RPC 2.0 over `stdio` and `SSE/HTTP`<br>• Automatic JSON Schema generation for MCP Tools<br>• Action documentation resources (`action://docs/{name}`)<br>• Prompt templates & auto-completions |

## Installation

```sh
go get github.com/nexssp/transportai@latest
```

## Quick Start: A2A Human-in-the-Loop (HITL) Server

```go
package main

import (
    "context"
    "os"
    "os/signal"

    "github.com/nexssp/kernel/action"
    "github.com/nexssp/transportai/ta2a"
)

func refundApprovalAgent() action.AnyAction {
    return action.New("refund.agent", func(_ context.Context, msg ta2a.Message) (ta2a.Task, error) {
        // Step 1: Initial query halts and requests human authorization
        if msg.Text == "request_refund" {
            return ta2a.Task{
                Status: ta2a.TaskStatusInputRequired,
                Text:   "Refund of $150 requires human approval",
                Artifacts: []ta2a.Artifact{
                    {Name: "ApprovalForm", Type: "form", Data: map[string]any{"amount": 150}},
                },
            }, nil
        }

        // Step 2: Resumed on the same contextId once human approves
        return ta2a.Task{
            Status: ta2a.TaskStatusCompleted,
            Text:   "Refund processed successfully: " + msg.Text,
        }, nil
    }).Route(ta2a.Role("refunds")).Build()
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    srv := ta2a.New(":8080", nil,
        ta2a.WithAgentCard(ta2a.AgentCard{
            Name:        "billing-support-agent",
            Description: "Processes customer billing and refund approvals",
            Version:     "1.0.0",
            Skills:      []string{"refunds", "invoices"},
        }),
    )
    srv.Mount([]action.AnyAction{refundApprovalAgent()})

    if _, err := srv.Do(ctx, nil); err != nil {
        panic(err)
    }
}
```
