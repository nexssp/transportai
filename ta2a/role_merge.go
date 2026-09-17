package ta2a

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
)

func mergeDefaults(dst, src any) {
	if src == nil || dst == nil {
		return
	}

	srcBytes, err := json.Marshal(src)
	if err != nil {
		return
	}

	var srcMap map[string]any
	if err = json.Unmarshal(srcBytes, &srcMap); err != nil {
		return
	}

	dstBytes, err := json.Marshal(dst)
	if err != nil {
		return
	}

	var dstMap map[string]any
	if err = json.Unmarshal(dstBytes, &dstMap); err != nil {
		dstMap = make(map[string]any)
	}

	for k, v := range srcMap {
		if _, exists := dstMap[k]; !exists || isZeroValue(dstMap[k]) {
			dstMap[k] = v
		}
	}

	mergedBytes, err := json.Marshal(dstMap)
	if err == nil {
		_ = json.Unmarshal(mergedBytes, dst) //nolint:errcheck // merged JSON was produced by json.Marshal from a compatible shape
	}
}

func mergeOverrides(dst, src any) {
	if src == nil || dst == nil {
		return
	}

	srcBytes, err := json.Marshal(src)
	if err != nil {
		return
	}

	var srcMap map[string]any
	if err = json.Unmarshal(srcBytes, &srcMap); err != nil {
		return
	}

	dstBytes, err := json.Marshal(dst)
	if err != nil {
		return
	}

	var dstMap map[string]any
	if err = json.Unmarshal(dstBytes, &dstMap); err != nil {
		dstMap = make(map[string]any)
	}

	maps.Copy(dstMap, srcMap)

	mergedBytes, err := json.Marshal(dstMap)
	if err == nil {
		_ = json.Unmarshal(mergedBytes, dst) //nolint:errcheck // merged JSON was produced by json.Marshal from a compatible shape
	}
}

func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case float64:
		return val == 0
	case bool:
		return !val
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	}
	return false
}

func extractStructField(obj any, fieldName string) (string, bool) {
	if obj == nil {
		return "", false
	}
	val := reflect.ValueOf(obj)
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return "", false
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return "", false
	}

	f := val.FieldByName(fieldName)
	if !f.IsValid() {
		return "", false
	}

	switch f.Kind() { //nolint:exhaustive // only reflect.String is targeted; other kinds intentionally ignored
	case reflect.String:
		return f.String(), true
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.Uint8 {
			return string(f.Bytes()), true
		}
	}
	return "", false
}

func applyTextToStruct(target any, text string) {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return
	}
	elem := val.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}

	for _, name := range []string{"Profile", "Text", "Input", "Query"} {
		f := elem.FieldByName(name)
		if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
			f.SetString(text)
			return
		}
	}
}

func evaluateCompositeTemplate(tmpl string, sources ...any) string {
	if tmpl == "" {
		return tmpl
	}

	combined := make(map[string]any)
	for _, src := range sources {
		if src == nil {
			continue
		}

		val := reflect.ValueOf(src)
		if val.Kind() == reflect.Pointer && !val.IsNil() {
			val = val.Elem()
		}
		if val.Kind() == reflect.Struct {
			typ := val.Type()
			for i := range val.NumField() {
				field := typ.Field(i)
				combined[field.Name] = val.Field(i).Interface()
			}
		}

		if d, err := json.Marshal(src); err == nil {
			var m map[string]any
			if json.Unmarshal(d, &m) == nil {
				maps.Copy(combined, m)
			}
		}
	}

	out := tmpl
	for k, v := range combined {
		valStr := fmt.Sprintf("%v", v)

		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", k), valStr)
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToUpper(k)), valStr)
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToLower(k)), valStr)

		noUnderscore := strings.ReplaceAll(k, "_", "")
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToUpper(noUnderscore)), valStr)
		out = strings.ReplaceAll(out, fmt.Sprintf("${%s}", strings.ToLower(noUnderscore)), valStr)
	}
	return out
}
