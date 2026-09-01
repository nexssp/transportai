package tmcp_test

import (
	"context"
	"strings"
	"testing"
)

func TestCompletionComplete(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "completion")
	srv.SetCompletionResolver(func(_ context.Context, refType, refName, argName, value string) ([]string, error) {
		if refType == "ref/prompt" && argName == "profile" {
			all := []string{"arch", "review", "compact", "test"}
			var matches []string
			for _, p := range all {
				if strings.HasPrefix(p, value) {
					matches = append(matches, p)
				}
			}
			return matches, nil
		}
		return nil, nil
	})

	client := newTestClient(t, srv)

	resp := client.Call("completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "review-prompt",
		},
		"argument": map[string]any{
			"name":  "profile",
			"value": "re",
		},
	}, 1)

	var result struct {
		Completion struct {
			Values []string `json:"values"`
		} `json:"completion"`
	}

	resp.BindResult(t, &result)

	if len(result.Completion.Values) != 1 || result.Completion.Values[0] != "review" {
		t.Fatalf("unexpected completion values: %+v", result.Completion.Values)
	}
}
