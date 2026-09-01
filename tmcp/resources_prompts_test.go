package tmcp_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/nexssp/transportai/tmcp"
)

func TestResourcesList_Empty(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "res-empty")
	client := newTestClient(t, srv)

	resp := client.Call("resources/list", nil, 1)

	var result struct {
		Resources []any `json:"resources"`
	}

	resp.BindResult(t, &result)

	if result.Resources == nil {
		t.Fatal("resources must be non-nil empty array")
	}
}

func TestResourcesList_WithResource(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "res")
	srv.RegisterResource(tmcp.Resource{
		URI:      "config://app",
		Name:     "App Config",
		MimeType: "text/plain",
		ReadFn: func(_ context.Context) (string, error) {
			return "debug=true", nil
		},
	})

	client := newTestClient(t, srv)

	resp := client.Call("resources/list", nil, 1)

	var result struct {
		Resources []struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"resources"`
	}

	resp.BindResult(t, &result)

	if len(result.Resources) != 1 || result.Resources[0].URI != "config://app" {
		t.Fatalf("unexpected resources: %+v", result.Resources)
	}
}

func TestResourcesRead(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "res")
	srv.RegisterResource(tmcp.Resource{
		URI:      "config://app",
		Name:     "App Config",
		MimeType: "text/plain",
		ReadFn: func(_ context.Context) (string, error) {
			return "debug=true", nil
		},
	})

	client := newTestClient(t, srv)

	resp := client.Call("resources/read", map[string]any{"uri": "config://app"}, 1)

	var result struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}

	resp.BindResult(t, &result)

	if len(result.Contents) != 1 || result.Contents[0].Text != "debug=true" {
		t.Fatalf("unexpected resource read: %+v", result)
	}
}

func TestResourcesRead_NotFound(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "res"))

	resp := client.Call("resources/read", map[string]any{"uri": "missing://uri"}, 1)
	if resp.Error == nil || resp.Error.Code != tmcp.CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp.Error)
	}
}

func TestResourceTemplatesList(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "templates")
	srv.RegisterTemplate(tmcp.ResourceTemplate{
		URITemplate: "orders://{id}",
		Name:        "Order",
	})

	client := newTestClient(t, srv)

	resp := client.Call("resources/templates/list", nil, 1)

	var result struct {
		Templates []struct {
			URITemplate string `json:"uriTemplate"`
			Name        string `json:"name"`
		} `json:"resourceTemplates"`
	}

	resp.BindResult(t, &result)

	if len(result.Templates) != 1 || result.Templates[0].Name != "Order" {
		t.Fatalf("unexpected templates: %+v", result.Templates)
	}
}

func TestPromptsList_Empty(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "prompts"))

	resp := client.Call("prompts/list", nil, 1)

	var result struct {
		Prompts []any `json:"prompts"`
	}

	resp.BindResult(t, &result)

	if result.Prompts == nil {
		t.Fatal("prompts must be non-nil empty array")
	}
}

func TestPromptsList_WithPrompt(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "prompts")
	srv.RegisterPrompt(tmcp.PromptTemplate{
		Name:        "review",
		Description: "Review code",
	})

	client := newTestClient(t, srv)

	resp := client.Call("prompts/list", nil, 1)

	var result struct {
		Prompts []struct {
			Name string `json:"name"`
		} `json:"prompts"`
	}

	resp.BindResult(t, &result)

	if len(result.Prompts) != 1 || result.Prompts[0].Name != "review" {
		t.Fatalf("unexpected prompts: %+v", result.Prompts)
	}
}

func TestPromptsGet(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "prompts")
	srv.RegisterPrompt(tmcp.PromptTemplate{
		Name: "review",
		BuildPrompt: func(_ context.Context, args map[string]string) (string, error) {
			return fmt.Sprintf("Review %s", args["file"]), nil
		},
	})

	client := newTestClient(t, srv)

	resp := client.Call("prompts/get", map[string]any{
		"name":      "review",
		"arguments": map[string]string{"file": "main.go"},
	}, 1)

	var result struct {
		Messages []struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}

	resp.BindResult(t, &result)

	if result.Messages[0].Content.Text != "Review main.go" {
		t.Fatalf("unexpected prompt text: %s", result.Messages[0].Content.Text)
	}
}

func TestPromptsGet_NotFound(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "prompts"))

	resp := client.Call("prompts/get", map[string]any{"name": "missing"}, 1)
	if resp.Error == nil || resp.Error.Code != tmcp.CodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp.Error)
	}
}

func TestResourceTemplatesList_Empty(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, newTestServer(t, "templates-empty"))

	resp := client.Call("resources/templates/list", nil, 1)

	var result struct {
		Templates []any `json:"resourceTemplates"`
	}

	resp.BindResult(t, &result)

	if result.Templates == nil {
		t.Fatal("resourceTemplates must be non-nil empty array")
	}
}
