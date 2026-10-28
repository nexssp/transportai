# Nexss Transport AI

<small>Nexss Flow 🌀 ready</small>

<!-- project-status -->

[![CI](https://github.com/nexssp/transportai/actions/workflows/ci.yml/badge.svg)](https://github.com/nexssp/transportai/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/nexssp/transportai.svg)](https://pkg.go.dev/github.com/nexssp/transportai)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

<!-- /project-status -->

Nexss Transport AI exposes [Nexss Kernel](https://github.com/nexssp/kernel) actions and [Nexss Flow](https://github.com/nexssp/flow) pipelines through AI-facing protocols. It provides an A2A-style HTTP agent transport and an MCP JSON-RPC transport over stdio or HTTP.

> **Scope:** These adapters connect actions and pipelines to protocol clients; they do not provide an AI model or agent runtime. A2A task and pending-approval state is held in process memory by default. The refund-review example in the [centralized Nexss examples repository](https://github.com/nexssp/examples) returns a manual-processing recommendation and does not issue a payment.

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

Requires **Go 1.27.3 or newer** (the version declared in `go.mod`). Flow listeners signal readiness after their HTTP listener binds; MCP stdio signals after its streams are initialized and before the first input read. Add the module to your application:

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

The action is exposed as the `math.sum` tool. For a runnable MCP server with structured results, see the [centralized Nexss examples repository](https://github.com/nexssp/examples).

## Quick start: A2A

For a runnable HTTP agent and an agent-card request, see the [centralized Nexss examples repository](https://github.com/nexssp/examples).

The `ta2a` adapter also supports message parts, task lifecycle operations, event streaming, callbacks, and human-in-the-loop pauses with continuation by `contextId`.

## Transports and packages

| Package | Purpose |
| --- | --- |
| [`ta2a`](ta2a/) | A2A-style HTTP agent cards, role-based action routing, messages and multipart content, task states, streaming, callbacks, and approval pauses. |
| [`tmcp`](tmcp/) | MCP JSON-RPC tools, resources, prompts, and completions over stdio or HTTP. Kernel actions can be mounted as tools. |
| [`nexssflow`](nexssflow/) | Combined Nexss Flow bundle for both adapters, including `.nflow` modifiers and listeners. |
| [`ta2a/nexssflow`](ta2a/nexssflow/) · [`tmcp/nexssflow`](tmcp/nexssflow/) | Modular Flow bundles when a program needs only one transport. |
| [Centralized Nexss examples](https://github.com/nexssp/examples) | Direct Go servers and Flow-based A2A and MCP examples. |

## Nexss Flow 🌀 integration

Use `@require` to load a transport bundle, bind a pipeline with its modifier, then start its listener. A2A serves HTTP. MCP serves stdio by default; set `addr` to serve MCP over HTTP instead.

### Expose a Flow pipeline as an A2A role

```
@require github.com/nexssp/transportai/ta2a/nexssflow { addr: ":8090", name: "greeting-agent" }

@pipeline greet
  :a2a="greeting"
  runtime.const @{ value: "Hello, Julia" }
@end

ta2a.listen
```

The A2A Flow bundle starts an HTTP listener on `addr` (default `:8090`). Check its agent card and send a message to the role exposed above:

```
curl -H "Authorization: Bearer ${NEXSS_A2A_HTTP_BEARER_TOKEN}" \
  http://localhost:8090/.well-known/agent-card.json
curl -X POST http://localhost:8090/message/send \
  -H "Authorization: Bearer ${NEXSS_A2A_HTTP_BEARER_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"message":{"role":"greeting","text":"Hi"}}'
```

All A2A HTTP routes—including the agent card, message send and stream, and task get and cancel—require an `Authorization: Bearer <token>` header. Set `NEXSS_A2A_HTTP_BEARER_TOKEN` in the listener process environment before starting it; startup fails closed if the variable is unset or not a valid Bearer token. Generate a high-entropy token without printing it, for example with `export NEXSS_A2A_HTTP_BEARER_TOKEN="$(openssl rand -hex 32)"`, and provide it to clients through their secure environment or secret store. Do not put the token in a `.nflow` file, source code, or logs. The same protection applies to the combined Go bundle when `nexssflow.Config.A2AAddr` is configured; its MCP listener remains stdio unless `MCPAddr` is set.
For Go clients, pass the token from trusted configuration with `ta2a.NewClient(baseURL, ta2a.WithBearerToken(token))` rather than embedding it in source code.

Use HTTPS or terminate TLS at a trusted proxy for traffic over untrusted networks; Bearer tokens are otherwise visible in transit.

### Expose a Flow pipeline as an MCP tool

```
@require github.com/nexssp/transportai/tmcp/nexssflow { name: "greeting-mcp" }

@pipeline greet
  :mcp="greeting.say"
  runtime.const @{ value: "Hello, Maks" }
@end

tmcp.listen
```

To make the Flow tool available to a remote MCP client over HTTP, set `addr` in the MCP bundle configuration:

```
@require github.com/nexssp/transportai/tmcp/nexssflow { name: "greeting-mcp", addr: ":8091" }

@pipeline greet
  :mcp="greeting.say"
  runtime.const @{ value: "Hello, Maks" }
@end

tmcp.listen
```

The HTTP listener exposes the adapter's SSE stream at `http://localhost:8091/mcp/sse` and accepts JSON-RPC POSTs at `/mcp/message`. For the combined `nexssflow` bundle, use `mcp_addr: ":8091"`. Both endpoints require an `Authorization: Bearer <token>` header. Set `NEXSS_MCP_HTTP_BEARER_TOKEN` in the listener process environment before starting the HTTP listener; if it is unset or not a valid Bearer token value, startup fails closed. For example, generate a high-entropy value without printing it with `export NEXSS_MCP_HTTP_BEARER_TOKEN="$(openssl rand -hex 32)"`, then provide it to the MCP client through that client's secure environment or secret store. Do not put the token in a `.nflow` file, source code, or logs. MCP stdio mode does not use or require this variable.

The combined `nexssflow` bundle is also available for embedded Flow hosts in Go. See [`nexssflow.NewBundle`](nexssflow/library.go) and the [centralized Nexss examples repository](https://github.com/nexssp/examples) for A2A refund-review and MCP ticket-triage examples covering configuration, approvals, structured schemas, and client requests. Flow MCP currently exposes tools; resource, prompt, and completion declarations remain available through the Go adapter rather than Flow modifiers.

## Runnable examples

Runnable Go and Flow examples are maintained in the [centralized Nexss examples repository](https://github.com/nexssp/examples). MCP clients can launch stdio examples or connect to the Flow HTTP listener; see the example READMEs for client setup and full requests. The Flow samples are self-contained and require no model API or external broker.

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
