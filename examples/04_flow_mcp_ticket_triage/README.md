# Flow MCP support-ticket triage

[`service.nflow`](service.nflow) is the workflow, not glue code: its `@schema TicketTriageRequest` declaration and `schema.validate` stage check required fields and datatypes before deterministic priority and queue rules run. It returns a response SLA plus a suggested next action. The `:mcp="support.ticket_triage"` modifier publishes the Flow pipeline under that MCP tool name. The sample does not call a ticketing system or change a ticket.

[`main.go`](main.go) embeds the workflow and starts Flow's MCP stdio server. Protocol messages use standard input/output; application logs and errors go to standard error so they do not corrupt the MCP stream.

## Build and connect

Build the server executable:

```sh
mkdir -p bin
go build -o ./bin/transportai-support-mcp ./examples/04_flow_mcp_ticket_triage
```

Configure an MCP client to launch the executable as a local stdio server. For clients that use a JSON server configuration, the entry has this shape (replace the path with an absolute path):

```json
{
  "mcpServers": {
    "support-ticket-triage": {
      "command": "/absolute/path/to/transportai/bin/transportai-support-mcp",
      "args": []
    }
  }
}
```

Set `MCP_NAME` and `MCP_VERSION` in the client process environment to override the advertised server metadata. Keep server output on stdout reserved for MCP protocol traffic.

## Try the tool

Call `support.ticket_triage` with structured arguments such as:

```json
{
  "ticket_id": "SUP-2048",
  "category": "billing",
  "urgency": "high",
  "summary": "Customer was charged twice for the annual plan"
}
```

The workflow returns a deterministic routing recommendation. `critical` urgency takes the P1/on-call path; billing and access cases receive their own queues; other categories use the general support path. Missing required fields or an unsupported urgency produce a tool error. This is a local rules example: connect real ticketing actions only after adding your own authentication, authorization, audit trail, and idempotency controls.

## Run manually

For a quick smoke test, send a newline-delimited MCP exchange on stdin; closing stdin ends the server and prints the protocol responses to stdout:

```sh
cat <<'EOF' | go run ./examples/04_flow_mcp_ticket_triage
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"support.ticket_triage","arguments":{"ticket_id":"SUP-2048","category":"billing","urgency":"high","summary":"Duplicate annual charge"}}}
EOF
```

In normal use, let the MCP client start and supervise the process.
