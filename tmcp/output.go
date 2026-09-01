package tmcp

import (
	"encoding"
	"encoding/json"
	"fmt"
)

// formatOutput handles conversion of kernel action results into clean text content.
func formatOutput(val any) string {
	if val == nil {
		return ""
	}

	switch v := val.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case encoding.TextMarshaler:
		if b, err := v.MarshalText(); err == nil {
			return string(b)
		}
	case fmt.Stringer:
		return v.String()
	}

	// Fallback to formatted JSON for complex domain structures
	b, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", val)
	}
	return string(b)
}
