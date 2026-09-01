package tmcp

import "context"

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ServerCapabilities struct {
	Tools       map[string]bool `json:"tools"`
	Resources   map[string]bool `json:"resources"`
	Prompts     map[string]bool `json:"prompts"`
	Logging     map[string]any  `json:"logging"`
	Completions map[string]any  `json:"completions"`
}

type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

type Resource struct {
	URI         string                                    `json:"uri"`
	Name        string                                    `json:"name"`
	Description string                                    `json:"description,omitempty"`
	MimeType    string                                    `json:"mimeType,omitempty"`
	ReadFn      func(ctx context.Context) (string, error) `json:"-"`
}

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type PromptTemplate struct {
	Name        string                                                            `json:"name"`
	Description string                                                            `json:"description,omitempty"`
	Arguments   []PromptArgument                                                  `json:"arguments,omitempty"`
	BuildPrompt func(ctx context.Context, args map[string]string) (string, error) `json:"-"`
}

type CompletionResolver func(ctx context.Context, refType, refName, argName, value string) ([]string, error)

type rpcHandler func(ctx context.Context, req Request) Response
