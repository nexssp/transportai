package ta2a_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/nexssp/transportai/ta2a"
)

const testHTTPBearerToken = "test-only-not-secret"

func TestMain(m *testing.M) {
	if err := os.Setenv(ta2a.HTTPBearerTokenEnv, testHTTPBearerToken); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func authenticatedTestHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) == 0 {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+testHTTPBearerToken)
		}
		next.ServeHTTP(w, r)
	})
}
