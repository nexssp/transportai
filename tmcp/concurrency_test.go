package tmcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nexssp/transportai/tmcp"
)

func TestConcurrentRegistration(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("concurrent", "1.0.0")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			uri := fmt.Sprintf("resource://%d", n)

			srv.RegisterResource(tmcp.Resource{URI: uri, Name: uri})
			srv.RegisterTemplate(tmcp.ResourceTemplate{
				URITemplate: fmt.Sprintf("templates://%d", n),
				Name:        fmt.Sprintf("T%d", n),
			})
			srv.RegisterPrompt(tmcp.PromptTemplate{
				Name: fmt.Sprintf("prompt-%d", n),
			})
			srv.SetCompletionResolver(func(context.Context, string, string, string, string) ([]string, error) {
				return nil, nil
			})
		}(i)
	}

	wg.Wait()
}

func TestConcurrentHTTPRequests(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, "http-concurrent", addAction())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(n int) {
			defer wg.Done()

			payload, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      n,
				"method":  "tools/call",
				"params": map[string]any{
					"name": "math.add",
					"arguments": map[string]any{
						"a": n,
						"b": 1,
					},
				},
			})

			resp, err := http.Post(ts.URL+"/mcp/message", "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Errorf("request %d failed: %v", n, err)
				return
			}
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: expected 200, got %d", n, resp.StatusCode)
			}
		}(i)
	}

	wg.Wait()
}
