package tmcp

import (
	"reflect"
	"testing"
	"time"
)

type textDoc struct {
	Markdown string
}

func (t textDoc) MarshalText() ([]byte, error) {
	return []byte(t.Markdown), nil
}

type stringer string

func (s stringer) String() string { return string(s) }

func TestFormatOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  any
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"bytes", []byte("hello"), "hello"},
		{"text marshaler", textDoc{Markdown: "# Doc"}, "# Doc"},
		{"stringer", stringer("str"), "str"},
		{
			"json fallback",
			struct {
				Value string `json:"value"`
			}{Value: "x"},
			`{"value":"x"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatOutput(tt.val); got != tt.want {
				t.Fatalf("formatOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

type schemaAddress struct {
	City string `json:"city" validate:"required"`
	Zip  string `json:"zip"`
}

type schemaPayload struct {
	CreatedAt time.Time      `json:"created_at"`
	Shipping  schemaAddress  `json:"shipping"`
	Tags      []string       `json:"tags"`
	Meta      map[string]int `json:"meta"`
	Ptr       *string        `json:"ptr"`
}

type recursiveNode struct {
	Next *recursiveNode `json:"next,omitempty"`
}

func TestBuildJSONSchema(t *testing.T) {
	t.Parallel()

	schema := buildJSONSchema(reflect.TypeFor[schemaPayload]())
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", schema["properties"])
	}

	createdAt, ok := props["created_at"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", props["created_at"])
	}
	if createdAt["type"] != "string" || createdAt["format"] != "date-time" {
		t.Fatalf("bad time schema: %+v", createdAt)
	}

	shipping, ok := props["shipping"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", props["shipping"])
	}
	if shipping["type"] != "object" {
		t.Fatalf("bad nested schema: %+v", shipping)
	}

	shippingProps, ok := shipping["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", shipping["properties"])
	}
	if _, found := shippingProps["city"]; !found {
		t.Fatalf("missing nested field: %+v", shippingProps)
	}

	req, ok := shipping["required"].([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", shipping["required"])
	}
	if len(req) != 1 || req[0] != "city" {
		t.Fatalf("expected required [city], got %v", req)
	}

	tags, ok := props["tags"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", props["tags"])
	}
	tagsItems, ok := tags["items"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", tags["items"])
	}
	if tags["type"] != "array" || tagsItems["type"] != "string" {
		t.Fatalf("bad array schema: %+v", tags)
	}

	meta, ok := props["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", props["meta"])
	}
	if meta["type"] != "object" {
		t.Fatalf("bad map schema: %+v", meta)
	}

	ptr, ok := props["ptr"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", props["ptr"])
	}
	if ptr["type"] != "string" {
		t.Fatalf("bad pointer schema: %+v", ptr)
	}
}

func TestBuildJSONSchema_Recursive(t *testing.T) {
	t.Parallel()

	schema := buildJSONSchema(reflect.TypeFor[recursiveNode]())
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", schema["properties"])
	}
	next, ok := props["next"].(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", props["next"])
	}

	if next["type"] != "object" {
		t.Fatalf("expected object for recursive type, got %+v", next)
	}
}
