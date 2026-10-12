package ta2a

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

func evaluateCompositeTemplate(source string, values ...any) string {
	if source == "" || len(values) == 0 {
		return source
	}
	merged := make(map[string]any)
	for index, value := range values {
		if value == nil {
			continue
		}
		merged[fmt.Sprintf("_%d", index)] = value
		if m, ok := value.(map[string]any); ok {
			maps.Copy(merged, m)
		}
		addTemplateValues(merged, value)
	}

	result := source
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		aliases := []string{key, strings.ToUpper(key), strings.ToLower(key), strings.ReplaceAll(key, "_", "")}
		for _, alias := range aliases {
			result = strings.ReplaceAll(result, "${"+alias+"}", fmt.Sprint(merged[key]))
		}
	}
	if !strings.Contains(result, "{{") {
		return result
	}
	parsed, err := template.New("composite").Option("missingkey=zero").Parse(result)
	if err != nil {
		return source
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, merged); err != nil {
		return source
	}
	return output.String()
}

func extractStructField(value any, fieldName string) (string, bool) {
	if value == nil {
		return "", false
	}
	if fieldName == "" {
		if text, ok := value.(string); ok {
			return text, true
		}
	}
	reflected, ok := indirectValue(reflect.ValueOf(value))
	if !ok {
		return "", false
	}
	if reflected.Kind() == reflect.Map {
		return extractMapField(reflected, fieldName)
	}
	if reflected.Kind() != reflect.Struct {
		return "", false
	}
	return extractReflectField(reflected, fieldName)
}

func indirectValue(value reflect.Value) (reflect.Value, bool) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	return value, value.IsValid()
}

func extractMapField(value reflect.Value, name string) (string, bool) {
	for _, key := range value.MapKeys() {
		if key.Kind() != reflect.String || !strings.EqualFold(key.String(), name) {
			continue
		}
		field := value.MapIndex(key)
		if field.IsValid() && field.CanInterface() {
			return fmt.Sprint(field.Interface()), true
		}
	}
	return "", false
}

func extractReflectField(value reflect.Value, name string) (string, bool) {
	typ := value.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		jsonTag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || !strings.EqualFold(field.Name, name) && (jsonTag == "" || jsonTag == "-" || !strings.EqualFold(jsonTag, name)) {
			continue
		}
		fieldValue := value.Field(i)
		if fieldValue.CanInterface() {
			return fmt.Sprint(fieldValue.Interface()), true
		}
	}
	return "", false
}

func addTemplateValues(target map[string]any, value any) {
	reflected, ok := indirectValue(reflect.ValueOf(value))
	if !ok {
		return
	}
	if reflected.Kind() == reflect.Map {
		for _, key := range reflected.MapKeys() {
			if key.Kind() != reflect.String {
				continue
			}
			item := reflected.MapIndex(key)
			if item.IsValid() && item.CanInterface() {
				addTemplateAliases(target, key.String(), item.Interface())
			}
		}
		return
	}
	if reflected.Kind() != reflect.Struct {
		return
	}
	typ := reflected.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		item := reflected.Field(i)
		if !field.IsExported() || !item.CanInterface() {
			continue
		}
		fieldValue := item.Interface()
		addTemplateAliases(target, field.Name, fieldValue)
		if jsonTag, _, _ := strings.Cut(field.Tag.Get("json"), ","); jsonTag != "" && jsonTag != "-" {
			addTemplateAliases(target, jsonTag, fieldValue)
		}
	}
}

func addTemplateAliases(target map[string]any, name string, value any) {
	target[name] = value
	snake := snakeCase(name)
	target[snake] = value
	target[strings.ToUpper(snake)] = value
	target[strings.ToLower(name)] = value
}

func snakeCase(value string) string {
	var result strings.Builder
	for index, current := range value {
		if unicode.IsUpper(current) && index > 0 {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToLower(current))
	}
	return result.String()
}

func applyTextToStruct(target any, text string) {
	if target == nil {
		return
	}
	reflected := reflect.ValueOf(target)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return
	}
	element := reflected.Elem()
	if element.Kind() == reflect.String {
		element.SetString(text)
		return
	}
	if element.Kind() != reflect.Struct {
		return
	}
	typ := element.Type()
	for _, candidate := range []string{"Profile", "Text", "Input", "Query", "Content", "Message", "Prompt"} {
		for i := range typ.NumField() {
			field := typ.Field(i)
			value := element.Field(i)
			if !value.CanSet() || value.Kind() != reflect.String {
				continue
			}
			jsonTag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if strings.EqualFold(field.Name, candidate) || strings.EqualFold(jsonTag, candidate) {
				value.SetString(text)
				return
			}
		}
	}
}
