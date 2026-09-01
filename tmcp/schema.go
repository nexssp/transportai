package tmcp

import (
	"reflect"
	"strings"
	"time"
)

// buildJSONSchema generates standard JSON Schema object definitions for action payloads.
func buildJSONSchema(t reflect.Type) map[string]any {
	return buildJSONSchemaVisited(t, map[reflect.Type]bool{})
}

func buildJSONSchemaVisited(t reflect.Type, visiting map[reflect.Type]bool) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Handle time.Time explicitly
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}
	}

	switch t.Kind() {
	case reflect.Struct:
		if visiting[t] {
			return map[string]any{"type": "object"}
		}
		visiting[t] = true
		defer delete(visiting, t)

		properties := make(map[string]any)
		var required []string

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}

			jsonTag := strings.Split(f.Tag.Get("json"), ",")[0]
			if jsonTag == "-" {
				continue
			}
			if jsonTag == "" {
				jsonTag = f.Name
			}

			if strings.Contains(f.Tag.Get("validate"), "required") {
				required = append(required, jsonTag)
			}

			properties[jsonTag] = buildJSONSchemaVisited(f.Type, visiting)
		}

		out := map[string]any{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			out["required"] = required
		}
		return out

	case reflect.Slice, reflect.Array:
		return map[string]any{
			"type":  "array",
			"items": buildJSONSchemaVisited(t.Elem(), visiting),
		}

	case reflect.Map:
		return map[string]any{
			"type":                 "object",
			"additionalProperties": buildJSONSchemaVisited(t.Elem(), visiting),
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	default:
		return map[string]any{"type": "string"}
	}
}
