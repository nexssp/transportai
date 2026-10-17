package ta2a_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transportai/ta2a"
)

func TestA2AHTTPBearerAuthenticationProtectsEveryRoute(t *testing.T) {
	routes := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "agent card",
			method:     http.MethodGet,
			path:       "/.well-known/agent-card.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "message send",
			method:     http.MethodPost,
			path:       "/message/send",
			body:       `{"message":{"role":"assistant","text":"Hi"}}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "message stream",
			method:     http.MethodPost,
			path:       "/message/stream",
			body:       `{"message":{"role":"assistant","text":"Hi"}}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "task get",
			method:     http.MethodPost,
			path:       "/tasks/get",
			body:       `{"id":"missing-task"}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "task cancel",
			method:     http.MethodPost,
			path:       "/tasks/cancel",
			body:       `{"id":"missing-task"}`,
			wantStatus: http.StatusNotFound,
		},
	}
	invalidAuthorization := []struct {
		name    string
		headers []string
	}{
		{name: "missing"},
		{name: "wrong scheme", headers: []string{"Basic " + testHTTPBearerToken}},
		{name: "wrong token", headers: []string{"Bearer wrong-token"}},
		{name: "duplicate headers", headers: []string{"Bearer " + testHTTPBearerToken, "Bearer wrong-token"}},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			t.Run("rejects invalid credentials", func(t *testing.T) {
				for _, credential := range invalidAuthorization {
					t.Run(credential.name, func(t *testing.T) {
						server := newA2AAuthTestServer()
						recorder := httptest.NewRecorder()
						request := newA2AAuthTestRequest(route.method, route.path, route.body)
						for _, value := range credential.headers {
							request.Header.Add("Authorization", value)
						}
						server.Handler().ServeHTTP(recorder, request)
						if recorder.Code != http.StatusUnauthorized {
							t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
						}
						if recorder.Header().Get("WWW-Authenticate") == "" {
							t.Fatal("missing WWW-Authenticate response header")
						}
					})
				}
			})

			t.Run("accepts configured token", func(t *testing.T) {
				server := newA2AAuthTestServer()
				recorder := httptest.NewRecorder()
				request := newA2AAuthTestRequest(route.method, route.path, route.body)
				request.Header.Set("Authorization", "Bearer "+testHTTPBearerToken)
				server.Handler().ServeHTTP(recorder, request)
				if recorder.Code != route.wantStatus {
					t.Fatalf("status = %d, want %d; body=%s", recorder.Code, route.wantStatus, recorder.Body.String())
				}
			})
		})
	}
}

func TestA2AHTTPAuthenticationFailsClosedWhenTokenConfigurationIsInvalid(t *testing.T) {
	for _, token := range []struct {
		name  string
		value string
		unset bool
	}{
		{name: "unset", unset: true},
		{name: "empty", value: ""},
		{name: "whitespace", value: "   "},
		{name: "invalid characters", value: "not:a-valid-token"},
		{name: "padding without token characters", value: "==="},
	} {
		t.Run(token.name, func(t *testing.T) {
			if token.unset {
				unsetTestEnvironmentVariable(t, ta2a.HTTPBearerTokenEnv)
			} else {
				t.Setenv(ta2a.HTTPBearerTokenEnv, token.value)
			}

			server := ta2a.New(":0", nil)
			recorder := httptest.NewRecorder()
			request := newA2AAuthTestRequest(http.MethodGet, "/.well-known/agent-card.json", "")
			request.Header.Set("Authorization", "Bearer "+testHTTPBearerToken)
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("request status = %d, want %d; body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
			}

			_, err := ta2a.New(":0", nil).Do(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), ta2a.HTTPBearerTokenEnv) {
				t.Fatalf("Do error = %v, want fail-closed configuration error mentioning %s", err, ta2a.HTTPBearerTokenEnv)
			}
		})
	}
}

func newA2AAuthTestServer() *ta2a.Transport {
	server := ta2a.New(":0", nil)
	server.Mount([]action.AnyAction{assistantAction()})
	return server
}

func newA2AAuthTestRequest(method, path, body string) *http.Request {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func unsetTestEnvironmentVariable(t *testing.T, key string) {
	t.Helper()
	previous, wasSet := os.LookupEnv(key)
	if wasSet {
		t.Setenv(key, previous)
	}
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}
