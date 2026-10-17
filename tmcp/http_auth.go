package tmcp

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
)

// HTTPBearerTokenEnv names the environment variable used to authenticate MCP HTTP requests.
const HTTPBearerTokenEnv = "NEXSS_MCP_HTTP_BEARER_TOKEN" // #nosec G101 -- environment-variable name, not a credential

func loadHTTPBearerToken() (string, error) {
	token, ok := os.LookupEnv(HTTPBearerTokenEnv)
	if !ok || !validBearerToken(token) {
		return "", errors.New(HTTPBearerTokenEnv + " must be set to a valid Bearer token for MCP HTTP")
	}
	return token, nil
}

func validBearerToken(token string) bool {
	if token == "" {
		return false
	}
	padding := false
	for _, char := range token {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', strings.ContainsRune("-._~+/", char):
			if padding {
				return false
			}
		case char == '=':
			padding = true
		default:
			return false
		}
	}
	return true
}

func (t *Transport) httpHandler(next http.Handler) http.Handler {
	token, err := loadHTTPBearerToken()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "MCP HTTP authentication is not configured", http.StatusServiceUnavailable)
		})
	}
	return bearerAuth(token, next)
}

func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
