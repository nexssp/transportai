package tmcp

import (
	"context"
	"encoding/json"
	"fmt"
)

func (t *Transport) handlePromptsList(_ context.Context, req Request) Response {
	t.mu.RLock()
	defer t.mu.RUnlock()

	list := make([]PromptTemplate, 0, len(t.prompts))
	for _, p := range t.prompts {
		list = append(list, p)
	}
	return successResponse(req.ID, map[string]any{"prompts": list})
}

func (t *Transport) handlePromptsGet(ctx context.Context, req Request) Response {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, CodeInvalidParams, "Invalid parameters")
	}

	t.mu.RLock()
	prompt, ok := t.prompts[p.Name]
	t.mu.RUnlock()

	if !ok || prompt.BuildPrompt == nil {
		return errorResponse(req.ID, CodeInvalidParams, fmt.Sprintf("Prompt %q not found", p.Name))
	}

	rendered, err := prompt.BuildPrompt(ctx, p.Arguments)
	if err != nil {
		return errorResponse(req.ID, CodeInternalError, err.Error())
	}

	return successResponse(req.ID, map[string]any{
		"description": prompt.Description,
		"messages": []map[string]any{
			{"role": "user", "content": ContentBlock{Type: "text", Text: rendered}},
		},
	})
}
