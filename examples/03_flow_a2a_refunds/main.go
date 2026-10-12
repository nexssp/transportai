package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/nexssp/flow/native"
	"github.com/nexssp/flow/runner"

	"github.com/nexssp/transportai/nexssflow"
)

//go:embed service.nflow
var flowSource embed.FS

const serviceName = "refund-review-agent"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "flow-a2a-refunds:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := envOrDefault("A2A_ADDR", ":8090")
	name := envOrDefault("A2A_NAME", serviceName)
	bundle, err := nexssflow.NewBundle(nexssflow.Config{
		A2AAddr: addr,
		A2AName: name,
	})
	if err != nil {
		return fmt.Errorf("configure transport AI Flow bundle: %w", err)
	}
	cfg, err := runner.BuildConfig(append(native.Bundles(), bundle))
	if err != nil {
		return fmt.Errorf("build Flow runtime: %w", err)
	}
	source, err := flowSource.ReadFile("service.nflow")
	if err != nil {
		return fmt.Errorf("read embedded Flow program: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("starting refund review agent", "address", addr, "agent", name)
	host := runner.NewHost()
	if ownErr := host.Own(bundle); ownErr != nil {
		return fmt.Errorf("assign Flow bundle lifecycle: %w", ownErr)
	}
	err = host.Run(ctx, func(runCtx context.Context) error {
		_, runErr := runner.Execute(runCtx, cfg, string(source), "service.nflow", nil)
		if errors.Is(runErr, context.Canceled) {
			return nil
		}
		return runErr
	})
	if err != nil {
		return fmt.Errorf("run Flow service: %w", err)
	}
	slog.Info("refund review agent stopped", "agent", name)
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
