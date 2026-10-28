package tmcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"golang.org/x/sync/errgroup"
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
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mcp HTTP server failed to listen: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = listener.Close()
		return err
	}
	contracts.SignalReady(ctx)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("mcp HTTP server crashed: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		select {
		case <-ctx.Done():
		case <-groupCtx.Done():
			if ctx.Err() == nil {
				return nil
			}
		}

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			t.logger.WarnContext(shutdownCtx, "mcp_http_shutdown_failed", "error", shutdownErr)
			if closeErr := server.Close(); closeErr != nil {
				return errors.Join(
					fmt.Errorf("mcp HTTP shutdown failed: %w", shutdownErr),
					fmt.Errorf("mcp HTTP close failed: %w", closeErr),
				)
			}
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return err
	}
	return ctx.Err()
}
