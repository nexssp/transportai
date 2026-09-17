package ta2a

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func normalizeMessageText(msg Message) string {
	if msg.Text != "" {
		return msg.Text
	}
	if len(msg.Parts) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, p := range msg.Parts {
		partText := ""
		switch p.Type {
		case PartText:
			partText = p.Text
		case PartFile:
			if p.File != nil {
				if p.File.URL != "" {
					partText = p.File.URL
				} else {
					partText = p.File.Name
				}
			}
		// PartData is strictly structured input — do NOT convert to text shorthand
		case PartData:
			continue
		}

		if partText != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(partText)
		}
	}

	return sb.String()
}

func autoExtractArtifacts(req, res any, binding AgentBinding) []Artifact {
	if res == nil {
		return nil
	}

	if provider, ok := res.(ArtifactProvider); ok {
		return provider.Artifacts()
	}

	existing := artifactsFromResult(res)
	if len(existing) > 0 {
		return existing
	}

	content, hasContent := extractStructField(res, "Content")
	if hasContent && content != "" {
		name := binding.ArtifactName
		if name != "" {
			name = evaluateCompositeTemplate(name, req, res)
		} else {
			profile, _ := extractStructField(res, "Profile")
			if profile == "" {
				profile, _ = extractStructField(req, "Profile")
			}
			if profile == "" {
				profile = "default"
			}
			name = fmt.Sprintf("context_%s.md", profile)
		}

		mimeType := binding.ArtifactMime
		if mimeType == "" {
			mimeType = "text/markdown"
		}

		return []Artifact{
			{
				Name:     name,
				Type:     "file",
				MimeType: mimeType,
				Data:     content,
			},
		}
	}

	return nil
}

func artifactsFromResult(res any) []Artifact {
	switch v := res.(type) {
	case Artifact:
		return []Artifact{v}
	case *Artifact:
		if v != nil {
			return []Artifact{*v}
		}
	case []Artifact:
		return v
	case []*Artifact:
		out := make([]Artifact, 0, len(v))
		for _, a := range v {
			if a != nil {
				out = append(out, *a)
			}
		}
		return out
	}
	return nil
}

func formatAgentResult(res any) string {
	if res == nil {
		return ""
	}

	val := reflect.ValueOf(res)
	if val.Kind() == reflect.Pointer && val.IsNil() {
		return ""
	}

	if s, ok := res.(string); ok {
		return s
	}
	if s, ok := res.(*string); ok {
		if s != nil {
			return *s
		}
		return ""
	}
	if b, ok := res.([]byte); ok {
		return string(b)
	}

	if tm, ok := res.(encoding.TextMarshaler); ok {
		if b, err := tm.MarshalText(); err == nil {
			return string(b)
		}
	}

	if s, ok := extractStructField(res, "Summary"); ok && s != "" {
		return s
	}

	if _, hasContent := extractStructField(res, "Content"); !hasContent {
		if st, ok := res.(fmt.Stringer); ok {
			return st.String()
		}
	}

	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", res)
	}
	return string(b)
}
