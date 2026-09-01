package tmcp

import (
	"context"
	"encoding/json"
	"fmt"
)

func (t *Transport) handleResourcesList(_ context.Context, req Request) Response {
	t.mu.RLock()
	defer t.mu.RUnlock()

	list := make([]Resource, 0, len(t.resources))
	for _, r := range t.resources {
		list = append(list, r)
	}
	return successResponse(req.ID, map[string]any{"resources": list})
}

func (t *Transport) handleResourceTemplatesList(_ context.Context, req Request) Response {
	t.mu.RLock()
	defer t.mu.RUnlock()

	list := t.templates
	if list == nil {
		list = make([]ResourceTemplate, 0)
	}
	return successResponse(req.ID, map[string]any{"resourceTemplates": list})
}

func (t *Transport) handleResourcesRead(ctx context.Context, req Request) Response {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, CodeInvalidParams, "Invalid parameters")
	}

	t.mu.RLock()
	res, ok := t.resources[p.URI]
	t.mu.RUnlock()

	if !ok || res.ReadFn == nil {
		return errorResponse(req.ID, CodeInvalidParams, fmt.Sprintf("Resource %q not found", p.URI))
	}

	text, err := res.ReadFn(ctx)
	if err != nil {
		return errorResponse(req.ID, CodeInternalError, err.Error())
	}

	return successResponse(req.ID, map[string]any{
		"contents": []map[string]any{
			{"uri": res.URI, "mimeType": res.MimeType, "text": text},
		},
	})
}
