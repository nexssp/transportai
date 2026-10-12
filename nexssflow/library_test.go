package nexssflow

import (
	"testing"

	"github.com/nexssp/flow/compiler"
	"github.com/nexssp/flow/native"
	"github.com/nexssp/flow/runner"

	"github.com/nexssp/transportai/tmcp"
)

func TestBundleRegistersAndComposesBothTransports(t *testing.T) {
	factory, ok := compiler.Lookup(ID)
	if !ok {
		t.Fatalf("Flow bundle %q was not registered", ID)
	}
	bundle := factory(nil)
	if bundle.ID != ID {
		t.Fatalf("bundle ID = %q, want %q", bundle.ID, ID)
	}
	if len(bundle.Libraries) != 2 {
		t.Fatalf("library count = %d, want two transport libraries", len(bundle.Libraries))
	}
	if len(bundle.Modifiers) != 8 {
		t.Fatalf("modifier count = %d, want 8 (7 A2A + 1 MCP)", len(bundle.Modifiers))
	}
	if _, err := compiler.NewDynamicResolver(bundle.Libraries...); err != nil {
		t.Fatalf("mount Flow libraries: %v", err)
	}
	if _, err := NewBundle(Config{}); err != nil {
		t.Fatalf("build default Flow bundle: %v", err)
	}
}

func TestMCPModifierCompilesToolBinding(t *testing.T) {
	bundle, err := NewBundle(Config{})
	if err != nil {
		t.Fatalf("build Flow bundle: %v", err)
	}
	cfg, err := runner.BuildConfig(append(native.Bundles(), bundle))
	if err != nil {
		t.Fatalf("build Flow config: %v", err)
	}
	const source = `@pipeline customer_lookup :mcp="customer.lookup"
  { customer_id: .customer_id }
@end`
	compiled, err := runner.Compile(t.Context(), cfg, source, "mcp-modifier-test.nflow")
	if err != nil {
		t.Fatalf("compile MCP-bound Flow pipeline: %v", err)
	}
	endpoint, ok := compiled.Resolver.Action("pipeline.customer_lookup")
	if !ok {
		t.Fatal("compiled resolver does not contain pipeline.customer_lookup")
	}
	for _, binding := range endpoint.GetBindings() {
		if _, ok := binding.(tmcp.ToolBinding); ok {
			return
		}
	}
	t.Fatalf("compiled action has no MCP tool binding: %T", endpoint)
}
