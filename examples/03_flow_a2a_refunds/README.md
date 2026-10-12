# Flow A2A refund review

Start with [`service.nflow`](service.nflow): it is the business workflow. Its `@schema RefundRequest` declaration and `schema.validate` stage check required fields and datatypes before the existing refund rules run. It applies a $1,000 review ceiling and pauses every refund at an explicit human-approval gate. Only after the caller sends `approve` does Flow return a recommendation marked `approved_for_manual_processing`; **the sample never issues a payment or calls a billing provider**.

The Go [`main.go`](main.go) is a small host for Flow's native actions, the transport bundle, and process shutdown. It embeds the exact `.nflow` source so the workflow ships with the executable.

## Run it

Requirements: Go 1.26+ and Task for the repository-wide quality commands. The example does not require a separate broker or model API.

```sh
# Optional: change the bind address or advertised agent name.
export A2A_ADDR=':8090'
export A2A_NAME='refund-review-agent'
go run ./examples/03_flow_a2a_refunds
```

The service exposes its agent card at `http://localhost:8090/.well-known/agent-card.json` and accepts A2A requests at `POST /message/send`.

## Submit a refund for review

Use a stable `contextId` for the approval conversation. The first request pauses before the Flow pipeline runs:

```sh
curl -sS http://localhost:8090/message/send \
  -H 'Content-Type: application/json' \
  -d '{"message":{"role":"billing.refund","contextId":"refund-1042","text":"refund","parts":[{"type":"data","data":{"ticket_id":"SUP-1042","customer_id":"cust-88","amount_cents":4599,"reason":"Duplicate annual renewal"}}]}}'
```

The response has status `input-required` and an `ApprovalForm` artifact. A manager continues the same conversation with:

```sh
curl -sS http://localhost:8090/message/send \
  -H 'Content-Type: application/json' \
  -d '{"message":{"role":"billing.refund","contextId":"refund-1042","text":"approve"}}'
```

The completed task contains the validated request, an `approved_for_manual_processing` disposition, and a reminder to issue the refund through the organization's billing system. Send `reject` instead to record a rejection. The example keeps pending approvals in the process memory; use a durable, authenticated approval store before adapting this sample for production.

## Host the bundle from Go

The top-level `github.com/nexssp/transportai/nexssflow` package registers both transport integrations with Flow and offers `NewBundle` for embedded hosts:

```go
bundle, err := nexssflow.NewBundle(nexssflow.Config{
    A2AAddr: ":8090",
    A2AName: "refund-review-agent",
})
if err != nil {
    return err
}
flowConfig, err := runner.BuildConfig(append(native.Bundles(), bundle))
if err != nil {
    return err
}
```

The same package also enables MCP with the `:mcp="tool-name"` modifier and `tmcp.listen`. A2A pipelines use `:a2a="role"`; add `:a2a_approval="role|trigger[,trigger...]|prompt"` to require a human decision before executing the pipeline.
