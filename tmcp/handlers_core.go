package tmcp

import (
	"context"
	"encoding/json"
)

func (t *Transport) handleInitialize(_ context.Context, req Request) Response {
	return successResponse(req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      t.serverInfo,
		Capabilities: ServerCapabilities{
			Tools:       map[string]bool{"listChanged": false},
			Resources:   map[string]bool{"subscribe": false, "listChanged": false},
			Prompts:     map[string]bool{"listChanged": false},
			Logging:     map[string]any{},
			Completions: map[string]any{},
		},
	})
}

func (t *Transport) handleNoop(_ context.Context, req Request) Response {
	return successResponse(req.ID, struct{}{})
}

func (t *Transport) handleSetLogLevel(_ context.Context, req Request) Response {
	var p struct {
		Level string `json:"level"`
	}
	_ = json.Unmarshal(req.Params, &p)
	if p.Level != "" {
		t.mu.Lock()
		t.logLevel = p.Level
		t.mu.Unlock()
	}
	return successResponse(req.ID, struct{}{})
}

func (t *Transport) handleCompletionComplete(ctx context.Context, req Request) Response {
	var p struct {
		Ref struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"ref"`
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, CodeInvalidParams, "Invalid parameters")
	}

	t.mu.RLock()
	resolver := t.completions
	t.mu.RUnlock()

	values := make([]string, 0)
	if resolver != nil {
		if vals, err := resolver(ctx, p.Ref.Type, p.Ref.Name, p.Argument.Name, p.Argument.Value); err == nil && vals != nil {
			values = vals
		}
	}

	return successResponse(req.ID, map[string]any{
		"completion": map[string]any{
			"values":  values,
			"hasMore": false,
		},
	})
}
