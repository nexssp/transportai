# nexss-transportai

AI-specific protocol transports for `nexss-kernel` actions.

```text
                  ┌─────────────────────────────────┐
                  │      nexss-kernel actions       │
                  └────────────────┬────────────────┘
                                   │
         ┌─────────────────────────┴─────────────────────────┐
         │                                                   │
         ▼                                                   ▼
   tmcp (Model Context Protocol)             ta2a (Agent2Agent Protocol)
   * Claude Desktop / Cursor / Windsurf       * Multi-Agent Cards
   * stdio & SSE/HTTP JSON-RPC                * Task lifecycle & streaming
```

## Supported Protocols

- **`tmcp`**: Model Context Protocol (MCP) server implementation over `stdio` and `SSE/HTTP`. Automatically exposes actions as MCP Tools and action-doc resources. Optional prompt templates can be registered with `RegisterPrompt` for Anthropic Claude Desktop, Cursor AI, Windsurf, and LLM orchestration frameworks.
- **`ta2a`**: Agent2Agent (A2A) protocol server exposing Agent Cards (`/.well-known/agent-card.json`), synchronous/asynchronous task lifecycles (`/message/send`, `/tasks/get`, `/tasks/cancel`), and multi-agent coordination.

## Installation

```sh
go get github.com/nexssp/transportai@latest
```

## Quick Start

### 1. Model Context Protocol (MCP) Server

```go
package main

import (
    "context"

    "github.com/nexssp/kernel/action"
    "github.com/nexssp/transportai/tmcp"
)

func main() {
    myTool := action.New("math.add", func(ctx context.Context, req struct{ A, B int }) (int, error) {
        return req.A + req.B, nil
    }).Description("Adds two integers together").Build()

    mcpServer := tmcp.New("my-math-server", "1.0.0")
    mcpServer.Mount([]action.AnyAction{myTool})

    // Serves standard JSON-RPC 2.0 via standard input/output
    if err := mcpServer.ServeStdio(context.Background()); err != nil {
        panic(err)
    }
}
```

### 2. Agent2Agent (A2A) Server

```go
package main

import (
    "context"
    "os"
    "os/signal"

    "github.com/nexssp/transportai/ta2a"
)

type MyAgent struct{}

func (a *MyAgent) Card(ctx context.Context) (ta2a.AgentCard, error) {
    return ta2a.AgentCard{Name: "billing-agent", Version: "1.0.0"}, nil
}

func (a *MyAgent) Send(ctx context.Context, msg ta2a.Message) (ta2a.Task, error) {
    return ta2a.Task{ID: "task-1", State: "completed", Text: "Invoice generated"}, nil
}

func (a *MyAgent) Get(ctx context.Context, id string) (ta2a.Task, error) {
    return ta2a.Task{ID: id, State: "completed"}, nil
}

func (a *MyAgent) Cancel(ctx context.Context, id string) error {
    return nil
}

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    srv := ta2a.New(":8080", &MyAgent{})
    if _, err := srv.Do(ctx, nil); err != nil {
        panic(err)
    }
}
```
