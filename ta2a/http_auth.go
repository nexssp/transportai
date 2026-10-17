package ta2a

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
)

// HTTPBearerTokenEnv names the environment variable used to authenticate A2A HTTP requests.
const HTTPBearerTokenEnv = "NEXSS_A2A_HTTP_BEARER_TOKEN" // #nosec G101 -- environment-variable name, not a credential

func loadHTTPBearerToken() (string, error) {
	token, ok := os.LookupEnv(HTTPBearerTokenEnv)
	if !ok || !validBearerToken(token) {
		return "", errors.New(HTTPBearerTokenEnv + " must be set to a valid Bearer token for A2A HTTP")
	}
	return token, nil
}

func validBearerToken(token string) bool {
	if token == "" {
		return false
	}

	padding := false
	hasBaseCharacter := false
	for _, char := range token {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', strings.ContainsRune("-._~+/", char):
			if padding {
				return false
			}
			hasBaseCharacter = true
		case char == '=':
			if !hasBaseCharacter {
				return false
			}
			padding = true
		default:
			return false
		}
	}

	return hasBaseCharacter
}

func hasValidBearerAuthorization(r *http.Request, token string) bool {
	authorization := r.Header.Values("Authorization")
	if len(authorization) != 1 {
		return false
	}

	parts := strings.Fields(authorization[0])
	return len(parts) == 2 &&
		strings.EqualFold(parts[0], "Bearer") &&
		subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1
}
