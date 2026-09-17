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
		return map[string]any{jsonSchemaKeyType: jsonSchemaTypeString, "format": "date-time"}
	}

	switch t.Kind() { //nolint:exhaustive // default branch handles all unlisted kinds
	case reflect.Struct:
		if visiting[t] {
			return map[string]any{jsonSchemaKeyType: jsonSchemaTypeObject}
		}
		visiting[t] = true
		defer delete(visiting, t)

		properties := make(map[string]any)
		var required []string

		for f := range t.Fields() {
			if !f.IsExported() {
				continue
			}

			jsonTag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
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
			jsonSchemaKeyType:       jsonSchemaTypeObject,
			jsonSchemaPropertiesKey: properties,
		}
		if len(required) > 0 {
			out["required"] = required
		}
		return out

	case reflect.Slice, reflect.Array:
		return map[string]any{
			jsonSchemaKeyType: "array",
			"items":           buildJSONSchemaVisited(t.Elem(), visiting),
		}

	case reflect.Map:
		return map[string]any{
			jsonSchemaKeyType:      jsonSchemaTypeObject,
			"additionalProperties": buildJSONSchemaVisited(t.Elem(), visiting),
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return map[string]any{jsonSchemaKeyType: "number"}

	case reflect.Bool:
		return map[string]any{jsonSchemaKeyType: "boolean"}

	default:
		return map[string]any{jsonSchemaKeyType: jsonSchemaTypeString}
	}
}

// BuildInputSchema converts a typed request payload into a JSON Schema object.
//
// It uses:
// - `json` tags for field names
// - `usage` tags for property descriptions
// - `enum` tags for allowed values
func BuildInputSchema(req any) map[string]any {
	properties := make(map[string]any)

	if req != nil {
		val := reflect.ValueOf(req)

		if val.Kind() == reflect.Pointer {
			if val.IsNil() {
				return map[string]any{
					jsonSchemaKeyType:       jsonSchemaTypeObject,
					jsonSchemaPropertiesKey: properties,
				}
			}
			val = val.Elem()
		}

		if val.Kind() == reflect.Struct {
			typ := val.Type()

			for field := range typ.Fields() {
				name := jsonName(field)

				if name == "" {
					continue
				}

				properties[name] = propertySchema(field)
			}
		}
	}

	return map[string]any{
		jsonSchemaKeyType:       jsonSchemaTypeObject,
		jsonSchemaPropertiesKey: properties,
	}
}

func jsonName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")

	if name == "" || name == "-" {
		name = strings.ToLower(field.Name)
	}

	return name
}

func propertySchema(field reflect.StructField) map[string]any {
	prop := map[string]any{}

	if usage := field.Tag.Get("usage"); usage != "" {
		prop["description"] = usage
	}

	if enum := field.Tag.Get("enum"); enum != "" {
		prop["enum"] = enumValues(enum)
	}

	if field.Type == reflect.TypeFor[time.Time]() {
		prop[jsonSchemaKeyType] = jsonSchemaTypeString
		prop["format"] = "date-time"
		return prop
	}

	switch field.Type.Kind() { //nolint:exhaustive // default handles all unlisted kinds
	case reflect.String:
		prop[jsonSchemaKeyType] = jsonSchemaTypeString

	case reflect.Bool:
		prop[jsonSchemaKeyType] = "boolean"

	case reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64:
		prop[jsonSchemaKeyType] = "integer"

	case reflect.Float32, reflect.Float64:
		prop[jsonSchemaKeyType] = "number"

	case reflect.Slice:
		if elem := field.Type.Elem(); elem.Kind() == reflect.String {
			prop[jsonSchemaKeyType] = "array"
			prop["items"] = map[string]any{
				jsonSchemaKeyType: jsonSchemaTypeString,
			}
		}
	}

	return prop
}

func enumValues(tag string) []string {
	rawValues := strings.Split(tag, ",")

	values := make([]string, 0, len(rawValues))
	seen := make(map[string]bool, len(rawValues))

	for _, value := range rawValues {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}

	return values
}
