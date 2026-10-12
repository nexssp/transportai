package ta2a

import (
	"maps"
	"reflect"
	"strings"

	"github.com/nexssp/kernel/action"
)

// mergeDefaults fills zero fields of dst with non-zero fields from src.
// Both must be same-struct-typed pointers OR dst struct + src map[string]any.
func mergeDefaults(dst, src any) {
	dv := derefStruct(dst)
	if !dv.IsValid() {
		return
	}
	if sv := derefStruct(src); sv.IsValid() && sv.Type() == dv.Type() {
		for i := range dv.NumField() {
			df, sf := dv.Field(i), sv.Field(i)
			if df.CanSet() && df.IsZero() && !sf.IsZero() {
				df.Set(sf)
			}
		}
		return
	}
	if values, ok := src.(map[string]any); ok {
		applyMapToStruct(dv, values, true)
	}
}

// mergeOverrides overwrites non-zero fields of dst with fields from src.
// src may be a struct value or map[string]any.
func mergeOverrides(dst, src any) {
	dv := derefStruct(dst)
	if !dv.IsValid() {
		return
	}
	if sv := derefStruct(src); sv.IsValid() && sv.Type() == dv.Type() {
		for i := range dv.NumField() {
			df, sf := dv.Field(i), sv.Field(i)
			if df.CanSet() && !sf.IsZero() {
				df.Set(sf)
			}
		}
		return
	}
	if values, ok := src.(map[string]any); ok {
		applyMapToStruct(dv, values, false)
	}
}

// applyMapToStruct writes map values into struct fields by JSON tag name.
func applyMapToStruct(dst reflect.Value, values map[string]any, onlyZero bool) {
	typ := dst.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		target := dst.Field(i)
		if !target.CanSet() || (onlyZero && !target.IsZero()) {
			continue
		}
		name := jsonName(field)
		if name == "" {
			continue
		}
		raw, exists := values[name]
		if !exists || raw == nil {
			continue
		}
		if assignReflect(target, raw) {
			continue
		}
		if err := action.Assign(target.Addr().Interface(), raw); err != nil {
			continue
		}
	}
}

//nolint:exhaustive // Direct assignment supports scalar JSON values; action.Assign handles other kinds.
func assignReflect(target reflect.Value, raw any) bool {
	switch target.Kind() {
	case reflect.String:
		value, ok := raw.(string)
		if ok {
			target.SetString(value)
		}
		return ok
	case reflect.Bool:
		value, ok := raw.(bool)
		if ok {
			target.SetBool(value)
		}
		return ok
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, ok := raw.(float64)
		if ok {
			target.SetInt(int64(value))
		}
		return ok
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value, ok := raw.(float64)
		if ok && value >= 0 {
			target.SetUint(uint64(value))
			return true
		}
		return false
	case reflect.Float32, reflect.Float64:
		value, ok := raw.(float64)
		if ok {
			target.SetFloat(value)
		}
		return ok
	default:
		return false
	}
}

func derefStruct(value any) reflect.Value {
	if value == nil {
		return reflect.Value{}
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return reflect.Value{}
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return reflected
}

func jsonName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		return strings.ToLower(field.Name)
	}
	return name
}

// applyAnyPayload adapts A2A's typed-message precedence to Flow's untyped input.
func applyAnyPayload(target any, message Message, binding AgentBinding, text string) bool {
	var payload map[string]any
	switch value := target.(type) {
	case *any:
		if value == nil {
			return false
		}
		payload = make(map[string]any)
		defer func() { *value = payload }()
	case *map[string]any:
		if value == nil {
			return false
		}
		payload = make(map[string]any)
		defer func() { *value = payload }()
	default:
		return false
	}

	mergeAnyDefaults(payload, binding.DefaultArgs)
	for _, part := range message.Parts {
		if part.Type == PartData {
			maps.Copy(payload, part.Data)
		}
	}
	if text != "" {
		payload["text"] = text
	}
	mergeAnyOverrides(payload, binding.Args)
	return true
}

func mergeAnyDefaults(target map[string]any, source any) {
	for key, value := range fieldsByJSONName(source, true) {
		if current, exists := target[key]; !exists || isZeroAny(current) {
			target[key] = value
		}
	}
}

func mergeAnyOverrides(target map[string]any, source any) {
	maps.Copy(target, fieldsByJSONName(source, true))
}

func fieldsByJSONName(source any, skipZero bool) map[string]any {
	if source == nil {
		return nil
	}
	reflected, ok := indirectValue(reflect.ValueOf(source))
	if !ok {
		return nil
	}
	values := make(map[string]any)
	if reflected.Kind() == reflect.Map {
		for _, key := range reflected.MapKeys() {
			if key.Kind() != reflect.String {
				continue
			}
			value := reflected.MapIndex(key)
			if value.IsValid() && value.CanInterface() {
				values[key.String()] = value.Interface()
			}
		}
		return values
	}
	if reflected.Kind() != reflect.Struct {
		return values
	}
	typ := reflected.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		value := reflected.Field(i)
		if !field.IsExported() || !value.CanInterface() || (skipZero && value.IsZero()) {
			continue
		}
		if name := jsonName(field); name != "" {
			values[name] = value.Interface()
		}
	}
	return values
}

func isZeroAny(value any) bool {
	if value == nil {
		return true
	}
	return reflect.ValueOf(value).IsZero()
}
