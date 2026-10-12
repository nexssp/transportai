// Package nexssflow registers the A2A and MCP transport bundles for Nexss Flow.
package nexssflow

import (
	"fmt"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/kernel/action"

	a2aflow "github.com/nexssp/transportai/ta2a/nexssflow"
	mcpflow "github.com/nexssp/transportai/tmcp/nexssflow"
)

// ID is the bundle name used by Flow's @require directive.
const ID = "transportai"

func init() {
	compiler.Register(ID, Bundle)
}

// Config controls the A2A and MCP listeners created by this Flow bundle.
type Config struct {
	A2AAddr    string `nflow:"a2a_addr"    default:":8090"`
	A2AName    string `nflow:"a2a_name"    default:"nexssflow-agent"`
	A2AVersion string `nflow:"a2a_version" default:"1.0.0"`
	MCPName    string `nflow:"mcp_name"    default:"nexssflow-mcp"`
	MCPVersion string `nflow:"mcp_version" default:"1.0.0"`
}

// Bundle builds both protocol adapters from Flow @require options.
func Bundle(options map[string]string) compiler.Bundle {
	cfg, err := compiler.Decode[Config](options)
	if err != nil {
		panic("transportai Flow bundle: " + err.Error())
	}
	bundle, err := NewBundle(cfg)
	if err != nil {
		panic("transportai Flow bundle: " + err.Error())
	}
	bundle.Symbols = []compiler.SymbolNamespace{{
		Name: "transportai",
		Symbols: map[string]any{
			"a2a_role.billing_refund": "billing.refund",
			"a2a_role.support_triage": "support.triage",
			"mcp_tool.ticket_triage":  "support.ticket_triage",
		},
	}}
	return bundle
}

// NewBundle builds both protocol adapters for an embedded Flow host.
func NewBundle(cfg Config) (compiler.Bundle, error) {
	a2aBundle, err := a2aflow.NewBundle(a2aflow.Config{
		Addr:    cfg.A2AAddr,
		Name:    cfg.A2AName,
		Version: cfg.A2AVersion,
	})
	if err != nil {
		return compiler.Bundle{}, fmt.Errorf("configure A2A Flow bundle: %w", err)
	}
	mcpBundle, err := mcpflow.NewBundle(mcpflow.Config{
		Name:    cfg.MCPName,
		Version: cfg.MCPVersion,
	})
	if err != nil {
		return compiler.Bundle{}, fmt.Errorf("configure MCP Flow bundle: %w", err)
	}

	return compiler.Bundle{
		ID:        ID,
		Libraries: append(append([]action.Library(nil), a2aBundle.Libraries...), mcpBundle.Libraries...),
		Modifiers: append(append([]compiler.Modifier(nil), a2aBundle.Modifiers...), mcpBundle.Modifiers...),
		Shutdowns: append(append([]compiler.ShutdownFunc(nil), a2aBundle.Shutdowns...), mcpBundle.Shutdowns...),
	}, nil
}
