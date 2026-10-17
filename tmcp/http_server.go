package tmcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ListenAndServe exposes the authenticated MCP HTTP handler on addr until ctx is canceled.
// The handler supports the legacy SSE endpoint at /mcp/sse and message posts at
// /mcp/message. No write timeout is set because SSE streams are long-lived.
func (t *Transport) ListenAndServe(ctx context.Context, addr string) error {
	if addr == "" {
		return errors.New("mcp HTTP listen address is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := loadHTTPBearerToken()
	if err != nil {
		return fmt.Errorf("mcp HTTP authentication is not configured: %w", err)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           bearerAuth(token, t.unprotectedHandler()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	stopped := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
				t.logger.WarnContext(shutdownCtx, "mcp_http_shutdown_failed", "error", shutdownErr)
				_ = server.Close()
			}
		case <-stopped:
		}
	}()

	err = server.ListenAndServe()
	close(stopped)
	if errors.Is(err, http.ErrServerClosed) {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("mcp HTTP server crashed: %w", err)
	}
	return nil
}
