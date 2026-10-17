package tmcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/transportai/tmcp"
)

const testHTTPBearerToken = "test-only-token-not-for-use"

func TestListenAndServe_HTTPAndCancellation(t *testing.T) {
	t.Setenv(tmcp.HTTPBearerTokenEnv, testHTTPBearerToken)

	listenConfig := net.ListenConfig{}
	reserved, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a test address: %v", err)
	}
	addr := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	srv := tmcp.New("http-flow", "1.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe(ctx, addr)
	}()

	client := &http.Client{Timeout: time.Second}
	endpoint := "http://" + addr + "/mcp/message"
	deadline := time.Now().Add(3 * time.Second)
	var response tmcp.Response
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if reqErr != nil {
			t.Fatalf("build request: %v", reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+testHTTPBearerToken)
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			if resp.StatusCode == http.StatusOK {
				if decodeErr := json.NewDecoder(resp.Body).Decode(&response); decodeErr != nil {
					_ = resp.Body.Close()
					t.Fatalf("decode JSON-RPC response: %v", decodeErr)
				}
				_ = resp.Body.Close()
				break
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if response.JSONRPC != "2.0" || response.ID == nil || response.Error != nil {
		t.Fatalf("unexpected HTTP JSON-RPC response: %+v", response)
	}

	cancel()
	select {
	case err := <-serveErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ListenAndServe error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP server did not stop after context cancellation")
	}
}

func TestHTTPHandlerRequiresBearerForEveryEndpoint(t *testing.T) {
	t.Setenv(tmcp.HTTPBearerTokenEnv, testHTTPBearerToken)

	server := httptest.NewServer(tmcp.New("auth", "1.0.0").Handler())
	defer server.Close()
	client := server.Client()

	endpoints := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "SSE stream", method: http.MethodGet, path: "/mcp/sse"},
		{name: "message post", method: http.MethodPost, path: "/mcp/message", body: `{"jsonrpc":"2.0","id":1,"method":"ping"}`},
	}
	for _, endpoint := range endpoints {
		for _, auth := range []struct {
			name   string
			header string
		}{
			{name: "missing"},
			{name: "wrong", header: "Bearer wrong-test-token"},
		} {
			t.Run(endpoint.name+"/"+auth.name, func(t *testing.T) {
				req, err := http.NewRequestWithContext(t.Context(), endpoint.method, server.URL+endpoint.path, strings.NewReader(endpoint.body))
				if err != nil {
					t.Fatalf("build request: %v", err)
				}
				if auth.header != "" {
					req.Header.Set("Authorization", auth.header)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("send request: %v", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
				}
				if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
					t.Fatalf("WWW-Authenticate = %q, want Bearer challenge", got)
				}
			})
		}
	}

	t.Run("valid message token", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp/message", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+testHTTPBearerToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("send request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("valid SSE token", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp/sse", http.NoBody)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+testHTTPBearerToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("send request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil || line != "event: endpoint\n" {
			t.Fatalf("first SSE line = %q, error = %v", line, err)
		}
	})
}

func TestListenAndServe_FailsClosedWithoutUsableToken(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "whitespace-only", token: "   "},
		{name: "malformed", token: "invalid:value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(tmcp.HTTPBearerTokenEnv, test.token)
			err := tmcp.New("http", "1.0.0").ListenAndServe(context.Background(), "127.0.0.1:0")
			if err == nil || !strings.Contains(err.Error(), tmcp.HTTPBearerTokenEnv) {
				t.Fatalf("ListenAndServe error = %v, want fail-closed configuration error", err)
			}
		})
	}
}

func TestListenAndServe_RequiresAddress(t *testing.T) {
	err := tmcp.New("http", "1.0.0").ListenAndServe(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("ListenAndServe error = %v, want a missing-address error", err)
	}
}
