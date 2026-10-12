package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nexssp/flow/native"
	"github.com/nexssp/flow/runner"

	"github.com/nexssp/transportai/nexssflow"
)

//go:embed service.nflow
var serviceFlow string

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	transportBundle, err := nexssflow.NewBundle(nexssflow.Config{
		A2AAddr:    ":8090",
		A2AName:    "ticket-triage-agent",
		A2AVersion: "1.0.0",
		MCPName:    "ticket-triage-mcp",
		MCPVersion: "1.0.0",
	})
	if err != nil {
		return fmt.Errorf("create transport bundle: %w", err)
	}

	cfg, err := runner.BuildConfig(append(native.Bundles(), transportBundle))
	if err != nil {
		return fmt.Errorf("build flow config: %w", err)
	}

	host := runner.NewHost()
	if err = host.Own(transportBundle); err != nil {
		return fmt.Errorf("own transport bundle: %w", err)
	}

	entrypoint := "pipeline.listen_mcp"
	if os.Getenv("MODE") == "a2a" {
		entrypoint = "pipeline.listen_a2a"
	}

	program := serviceFlow + "\n{} -> " + entrypoint

	return host.Run(ctx, func(runCtx context.Context) error {
		_, execErr := runner.Execute(runCtx, cfg, program, "entrypoint.nflow", nil)
		return execErr
	})
}
