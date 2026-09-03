package tmcp_test

import (
	"context"
	"testing"

	"github.com/nexssp/kernel/action"
)

type schemaToolReq struct {
	Profile string   `json:"profile" usage:"Content profile" enum:"arch,api,impl"`
	Tags    []string `json:"tags" usage:"Tags to apply"`
}

func TestToolsList_UsesUsageAndEnum(t *testing.T) {
	t.Parallel()

	act := action.New("schema.tool", func(_ context.Context, req schemaToolReq) (string, error) {
		return req.Profile, nil
	}).
		Description("Schema test tool").
		Build()

	srv := newTestServer(t, "schema-test", act)
	client := newTestClient(t, srv)

	resp := client.Call("tools/list", nil, 1)

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type       string `json:"type"`
				Properties map[string]struct {
					Type        string         `json:"type"`
					Description string         `json:"description"`
					Enum        []string       `json:"enum,omitempty"`
					Items       map[string]any `json:"items,omitempty"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}

	resp.BindResult(t, &result)

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}

	tool := result.Tools[0]

	if tool.Name != "schema.tool" {
		t.Fatalf("unexpected tool name: %s", tool.Name)
	}

	if tool.Description != "Schema test tool" {
		t.Fatalf("unexpected description: %s", tool.Description)
	}

	if tool.InputSchema.Type != "object" {
		t.Fatalf("expected object schema, got %q", tool.InputSchema.Type)
	}

	properties := tool.InputSchema.Properties

	profile, ok := properties["profile"]
	if !ok {
		t.Fatalf("missing profile property")
	}

	if profile.Type != "string" {
		t.Fatalf("profile type = %q, want string", profile.Type)
	}

	if profile.Description != "Content profile" {
		t.Fatalf("profile description = %q", profile.Description)
	}

	wantEnum := []string{"arch", "api", "impl"}
	if len(profile.Enum) != len(wantEnum) {
		t.Fatalf("profile enum = %v, want %v", profile.Enum, wantEnum)
	}
	for i := range wantEnum {
		if profile.Enum[i] != wantEnum[i] {
			t.Fatalf("profile enum = %v, want %v", profile.Enum, wantEnum)
		}
	}

	tags, ok := properties["tags"]
	if !ok {
		t.Fatalf("missing tags property")
	}

	if tags.Type != "array" {
		t.Fatalf("tags type = %q, want array", tags.Type)
	}

	if tags.Description != "Tags to apply" {
		t.Fatalf("tags description = %q", tags.Description)
	}

	if tags.Items["type"] != "string" {
		t.Fatalf("tags items = %v", tags.Items)
	}
}
