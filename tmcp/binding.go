package tmcp

// ToolBinding carries the public MCP tool configuration.
type ToolBinding struct {
	Name        string
	Description string
	InputSchema any
}

func (b ToolBinding) String() string { return "mcp: tool=" + b.Name }

// Tool returns a ToolBinding for the given tool name.
func Tool(name string) ToolBinding { return ToolBinding{Name: name} }

// WithDescription sets the description shown to MCP clients.
func (b ToolBinding) WithDescription(desc string) ToolBinding {
	b.Description = desc
	return b
}

// WithInputSchema attaches a pre-compiled JSON Schema object.
func (b ToolBinding) WithInputSchema(schema any) ToolBinding {
	b.InputSchema = schema
	return b
}
