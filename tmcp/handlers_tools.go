package tmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

func (t *Transport) handleToolsList(_ context.Context, req Request) Response {
	t.mu.RLock()
	defer t.mu.RUnlock()

	tools := make([]ToolDescriptor, 0, len(t.actions))
	for name, act := range t.actions {
		meta := act.Describe()
		var schema any = map[string]any{"type": "object", "properties": map[string]any{}}

		if typed, ok := act.(action.TypedPayload); ok {
			if reqPayload := typed.ReqPayload(); reqPayload != nil {
				schema = buildJSONSchema(reflect.TypeOf(reqPayload))
			}
		}

		desc := ""
		if meta != nil {
			desc = meta.Description
		}

		tools = append(tools, ToolDescriptor{
			Name:        name,
			Description: desc,
			InputSchema: schema,
		})
	}

	return successResponse(req.ID, map[string]any{"tools": tools})
}

func (t *Transport) handleToolsCall(ctx context.Context, req Request) Response {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, CodeInvalidParams, "Invalid parameters")
	}

	t.mu.RLock()
	act, ok := t.actions[p.Name]
	t.mu.RUnlock()

	if !ok {
		return errorResponse(req.ID, CodeMethodNotFound, fmt.Sprintf("Tool %q not found", p.Name))
	}

	exec, ok := act.(action.Executable)
	if !ok {
		return errorResponse(req.ID, CodeInternalError, "Action is not executable")
	}

	decodeFn := func(target any) error {
		if len(p.Arguments) > 0 && string(p.Arguments) != "null" && string(p.Arguments) != "{}" {
			return json.Unmarshal(p.Arguments, target)
		}
		return nil
	}

	res, err := exec.ExecuteDecoded(ctx, decodeFn)
	if err != nil {
		// Kernel action failures map to MCP isError responses
		appErr := xerr.From(err)
		return successResponse(req.ID, ToolCallResult{
			IsError: true,
			Content: []ContentBlock{{Type: "text", Text: appErr.Error()}},
		})
	}

	return successResponse(req.ID, ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: formatOutput(res)}},
	})
}
