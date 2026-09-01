package ta2a

import (
	"context"
	"net/http"
)

type Option func(*Transport)

func WithMaxBodyBytes(n int64) Option {
	return func(t *Transport) {
		if n > 0 {
			t.maxBodyBytes = n
		}
	}
}

func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(t *Transport) {
		t.mdwsMu.Lock()
		defer t.mdwsMu.Unlock()
		t.mdws = append(t.mdws, mw...)
	}
}

func WithAgentCardProvider(p AgentCardProvider) Option {
	return func(t *Transport) {
		t.cardProvider = p
	}
}

func WithAgentCard(card AgentCard) Option {
	return func(t *Transport) {
		t.cardProvider = AgentCardProviderFunc(func(context.Context) (AgentCard, error) {
			return card, nil
		})
	}
}

func WithAuth(v AuthVerifier) Option {
	return func(t *Transport) {
		t.authVerifier = v
	}
}

func WithWebhookSecret(secret string) Option {
	return func(t *Transport) {
		t.webhookSecret = secret
	}
}

// WithWebhookHTTPClient injects a custom HTTP client for outgoing webhook delivery (e.g. test isolation).
func WithWebhookHTTPClient(client *http.Client) Option {
	return func(t *Transport) {
		if client != nil {
			t.webhookClient = client
		}
	}
}
