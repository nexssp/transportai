# Nexss Transport AI

AI-facing protocol adapters for [Nexss Kernel](https://github.com/nexssp/kernel) actions and Nexss Flow pipelines. The repository contains two transports:

- **`ta2a`** — A2A-style agent cards, multipart messages, task lifecycle, streaming, and human-in-the-loop continuation.
- **`tmcp`** — MCP JSON-RPC tools, resources, prompts, completions, and stdio/HTTP serving.

These packages adapt Kernel actions; they do not replace the action/runtime layer. See the runnable examples for the supported request shapes. The A2A adapter implements the repository's A2A-style protocol surface; this README does not claim an independent conformance certification.

## Capabilities

| Package | What it provides |
|---|---|
| [`ta2a`](ta2a/) | Agent-card discovery; text, file, and data message parts; task states; role-based routing; input-required pauses and continuation by `contextId`; event streaming; callback support; an A2A client. |
| [`tmcp`](tmcp/) | MCP JSON-RPC over stdio and HTTP; Kernel actions as tools; resources and prompts; completion support; typed schemas and lifecycle handling. |
| [`nexssflow`](nexssflow/) | One Flow bundle that composes both transport adapters for `.nflow` programs and embedded Go hosts. |
| [`examples/03_flow_a2a_refunds`](examples/03_flow_a2a_refunds/) | Flow exposed as an A2A agent: validate a refund request, pause for manager approval, and return a manual-processing recommendation without issuing a payment. |
| [`examples/04_flow_mcp_ticket_triage`](examples/04_flow_mcp_ticket_triage/) | Flow exposed as an MCP tool: validate a support ticket and return a deterministic queue, priority, SLA, and next-step recommendation. |

## Requirements and installation

- Go **1.26+** (as declared in `go.mod`).
- [Task](https://taskfile.dev/) for the repository commands.
- `golangci-lint` v2 for the configured format, lint, and full `task check` steps.

For a Go installation, install the tools and add `$(go env GOPATH)/bin` to `PATH`:

```sh
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

Add the transport to another module with:

```sh
go get github.com/nexssp/transportai@latest
```

The module pins compatible Kernel, Flow, and transport dependencies in `go.mod`.

## Runnable examples

```sh
# MCP stdio server
go run ./examples/01_mcp_stdio_server

# A2A HTTP agent
go run ./examples/02_a2a_agent_server

# Flow .nflow A2A refund-review service
go run ./examples/03_flow_a2a_refunds

# Flow .nflow MCP stdio server (normally launched by an MCP client)
go run ./examples/04_flow_mcp_ticket_triage
```

The first two examples show direct transport use. Both Flow examples are self-contained and need no model API or external broker. The A2A service uses `A2A_ADDR` (default `:8090`) and `A2A_NAME` (default `refund-review-agent`). The MCP Flow service uses stdio and should normally be started by an MCP client; set `MCP_NAME` or `MCP_VERSION` to change its advertised metadata. See each example README for full requests and client setup.

## Nexss Flow integration

The examples contain real `.nflow` source embedded and run by small Go hosts. [`examples/03_flow_a2a_refunds/service.nflow`](examples/03_flow_a2a_refunds/service.nflow) shows Flow exposed through A2A, including an approval pause and continuation. [`examples/04_flow_mcp_ticket_triage/service.nflow`](examples/04_flow_mcp_ticket_triage/service.nflow) shows Flow exposed as an MCP tool, including request validation and structured tool output.

### A2A refund-review workflow

1. Requires a ticket ID, customer ID, and reason.
2. Enforces a positive refund amount no greater than **$1,000**.
3. Pauses the `billing.refund` role at a human approval gate.
4. Resumes on the same `contextId` after `approve` or `reject`.
5. On approval, returns `approved_for_manual_processing`; it does **not** perform the refund or call a billing system.

Run the service:

```sh
go run ./examples/03_flow_a2a_refunds
```

Submit a review request (use a stable `contextId` for the approval exchange):

```sh
curl -sS http://localhost:8090/message/send \
  -H 'Content-Type: application/json' \
  -d '{"message":{"role":"billing.refund","contextId":"refund-1042","text":"refund","parts":[{"type":"data","data":{"ticket_id":"SUP-1042","customer_id":"cust-88","amount_cents":4599,"reason":"Duplicate annual renewal"}}]}}'
```

The first response is `input-required` and includes an `ApprovalForm`. A manager continues the same conversation with:

```sh
curl -sS http://localhost:8090/message/send \
  -H 'Content-Type: application/json' \
  -d '{"message":{"role":"billing.refund","contextId":"refund-1042","text":"approve"}}'
```

The example keeps pending approvals in process memory and is intended as a practical starting point, not a production approval store. Use durable persistence, authenticated approver identity, authorization, audit retention, and concurrency controls before adapting this pattern for a live billing workflow.

### Embed Flow in Go

The root `nexssflow` package composes both protocol bundles. A service can build Flow's standard actions plus the transport bundle, then transfer bundle cleanup ownership to `runner.Host`:

```go
bundle, err := nexssflow.NewBundle(nexssflow.Config{
    A2AAddr: ":8090",
    A2AName: "refund-review-agent",
})
if err != nil {
    return err
}

cfg, err := runner.BuildConfig(append(native.Bundles(), bundle))
if err != nil {
    return err
}

host := runner.NewHost()
if err := host.Own(bundle); err != nil {
    return err
}
return host.Run(ctx, func(ctx context.Context) error {
    _, err := runner.Execute(ctx, cfg, source, "service.nflow", nil)
    return err
})
```

`nexssflow.Config` fields configure A2A address/name/version and MCP name/version. For modular use, import `github.com/nexssp/transportai/ta2a/nexssflow` or `github.com/nexssp/transportai/tmcp/nexssflow` directly. In Flow source, `:a2a="role"` binds an agent role; `:a2a_approval="role|trigger[,trigger...]|prompt"` adds the approval gate; `:mcp="tool-name"` publishes the pipeline under that MCP tool name. The matching `ta2a.listen` or `tmcp.listen` action mounts bound pipelines and serves the protocol.

## Development tasks

The default Task action **lists available tasks**; it does not silently run tests or modify files:

```sh
task --list
task check
```

`task check` runs the configured formatting check, `golangci-lint`, `go vet`, the full race-enabled test suite, and builds every runnable example. Individual tasks include `task fmt`, `task fmt:check`, `task lint`, `task test`, `task test:race`, `task test:mcp`, `task test:a2a`, `task examples`, and `task bench`.

The repository's [`Taskfile.yml`](Taskfile.yml) and [`.golangci.yml`](.golangci.yml) are the source of truth for those commands and lint settings.
