# Nexss Transport AI

<!-- project-status -->

[![CI](https://github.com/nexssp/transportai/actions/workflows/ci.yml/badge.svg)](https://github.com/nexssp/transportai/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/nexssp/transportai.svg)](https://pkg.go.dev/github.com/nexssp/transportai)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

<!-- /project-status -->

Nexss Transport AI exposes [Nexss Kernel](https://github.com/nexssp/kernel) actions and [Nexss Flow](https://github.com/nexssp/flow) pipelines through AI-facing protocols. It provides an A2A-style HTTP agent transport and an MCP JSON-RPC transport over stdio or HTTP.

> **Scope:** These adapters connect actions and pipelines to protocol clients; they do not provide an AI model or agent runtime. A2A task and pending-approval state is held in process memory by default. The refund-review example returns a manual-processing recommendation and does not issue a payment.

## Architecture

```
Go application / Nexss Flow
             │
             ▼
     Nexss Kernel actions
   .Route(ta2a.Role(...)) · .Route(tmcp.Tool(...))
             │
             ▼
    Nexss Transport AI
      ├── ta2a     A2A-style HTTP agent, tasks, streaming
      ├── tmcp     MCP JSON-RPC over stdio or HTTP
      └── nexssflow Flow bundles, modifiers, and listeners
```

## Install

Requires **Go 1.26.9 or newer** (the version declared in `go.mod`). Add the module to your application:

```
go get github.com/nexssp/transportai@latest
```

## Quick start: MCP

Create a typed Kernel action, mount it as an MCP tool, and serve the protocol on standard input/output:

```go
package main

import (
    "context"
    "log"

    "github.com/nexssp/kernel/action"
    "github.com/nexssp/transportai/tmcp"
)

type SumRequest struct {
    A int `json:"a"`
    B int `json:"b"`
}

func main() {
    sum := action.New("math.sum", func(_ context.Context, req SumRequest) (int, error) {
        return req.A + req.B, nil
    }).Build()

    server := tmcp.New("calculator", "1.0.0")
    server.Mount([]action.AnyAction{sum})
    if err := server.ServeStdio(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

The action is exposed as the `math.sum` tool. See [`examples/01_mcp_stdio_server`](examples/01_mcp_stdio_server/) for a runnable server with structured results.

## Quick start: A2A

Run the included HTTP agent and inspect its agent card:

```
go run ./examples/02_a2a_agent_server
curl http://localhost:8090/.well-known/agent-card.json
```

The `ta2a` adapter also supports message parts, task lifecycle operations, event streaming, callbacks, and human-in-the-loop pauses with continuation by `contextId`.

## Transports and packages

| Package | Purpose |
| --- | --- |
| [`ta2a`](ta2a/) | A2A-style HTTP agent cards, role-based action routing, messages and multipart content, task states, streaming, callbacks, and approval pauses. |
| [`tmcp`](tmcp/) | MCP JSON-RPC tools, resources, prompts, and completions over stdio or HTTP. Kernel actions can be mounted as tools. |
| [`nexssflow`](nexssflow/) | Combined Nexss Flow bundle for both adapters, including `.nflow` modifiers and listeners. |
| [`ta2a/nexssflow`](ta2a/nexssflow/) · [`tmcp/nexssflow`](tmcp/nexssflow/) | Modular Flow bundles when a program needs only one transport. |
| [`examples/`](examples/) | Direct Go servers and Flow-based A2A and MCP examples. |

## Nexss Flow integration

Use `@require` to load a transport bundle, bind a pipeline with its modifier, then start its listener. The A2A bundle serves HTTP; the MCP bundle serves stdio.

### Expose a Flow pipeline as an A2A role

```exs
@require github.com/nexssp/transportai/ta2a/nexssflow { addr: ":8090", name: "greeting-agent" }

@pipeline greet
  :a2a="greeting"
  runtime.const @{ value: "Hello, Julia" }
@end

ta2a.listen
```

### Expose a Flow pipeline as an MCP tool

```exs
@require github.com/nexssp/transportai/tmcp/nexssflow { name: "greeting-mcp" }

@pipeline greet
  :mcp="greeting.say"
  runtime.const @{ value: "Hello, Maks" }
@end

tmcp.listen
```

The combined `nexssflow` bundle is also available for embedded Flow hosts in Go. See [`nexssflow.NewBundle`](nexssflow/library.go) and the runnable [A2A refund-review](examples/03_flow_a2a_refunds/) and [MCP ticket-triage](examples/04_flow_mcp_ticket_triage/) examples for configuration, approvals, structured schemas, and client requests.

## Runnable examples

```
go run ./examples/01_mcp_stdio_server        # MCP stdio server
go run ./examples/02_a2a_agent_server       # A2A HTTP agent on :8090
go run ./examples/03_flow_a2a_refunds       # Flow A2A approval example
go run ./examples/04_flow_mcp_ticket_triage  # Flow MCP stdio server
```

The MCP examples are normally launched by an MCP client, which owns the stdio connection. The Flow samples are self-contained and require no model API or external broker; see each example's README for client setup and full requests.

## Development

The repository uses [Task](https://taskfile.dev). Install it if needed, then list the available targets and run checks:

```
go install github.com/go-task/task/v3/cmd/task@latest
task --list
task build
task test
task test:race
task lint
task ci
```

`task ci` runs lint and the race-enabled test suite. `task bench` runs the benchmarks; see [`Taskfile.yml`](Taskfile.yml) for all targets.
