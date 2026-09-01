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
			"{\n  \"value\": \"x\"\n}",
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

	schema := buildJSONSchema(reflect.TypeOf(schemaPayload{}))
	props := schema["properties"].(map[string]any)

	createdAt := props["created_at"].(map[string]any)
	if createdAt["type"] != "string" || createdAt["format"] != "date-time" {
		t.Fatalf("bad time schema: %+v", createdAt)
	}

	shipping := props["shipping"].(map[string]any)
	if shipping["type"] != "object" {
		t.Fatalf("bad nested schema: %+v", shipping)
	}
	shippingProps := shipping["properties"].(map[string]any)
	if _, ok := shippingProps["city"]; !ok {
		t.Fatalf("missing nested field: %+v", shippingProps)
	}
	if req := shipping["required"].([]string); len(req) != 1 || req[0] != "city" {
		t.Fatalf("expected required [city], got %v", req)
	}

	tags := props["tags"].(map[string]any)
	if tags["type"] != "array" || tags["items"].(map[string]any)["type"] != "string" {
		t.Fatalf("bad array schema: %+v", tags)
	}

	meta := props["meta"].(map[string]any)
	if meta["type"] != "object" {
		t.Fatalf("bad map schema: %+v", meta)
	}

	ptr := props["ptr"].(map[string]any)
	if ptr["type"] != "string" {
		t.Fatalf("bad pointer schema: %+v", ptr)
	}
}

func TestBuildJSONSchema_Recursive(t *testing.T) {
	t.Parallel()

	schema := buildJSONSchema(reflect.TypeOf(recursiveNode{}))
	props := schema["properties"].(map[string]any)
	next := props["next"].(map[string]any)

	if next["type"] != "object" {
		t.Fatalf("expected object for recursive type, got %+v", next)
	}
}
