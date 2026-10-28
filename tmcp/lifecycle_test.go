package tmcp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nexssp/flow/compiler/contracts"

	"github.com/nexssp/transportai/tmcp"
)

func TestServe_CanceledBeforeStart(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("cancel", "1.0.0")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	if err := srv.Serve(ctx, strings.NewReader(""), &out); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestServe_EOFAfterUnterminatedLine(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("eof", "1.0.0")

	var out bytes.Buffer
	payload := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	if err := srv.Serve(context.Background(), strings.NewReader(payload), &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("expected response for final unterminated line")
	}
}

func TestServeSignalsFlowReadyAfterStreamSetup(t *testing.T) {
	t.Parallel()

	ready := contracts.NewReadySignal()
	ctx := contracts.WithServiceReady(context.Background(), ready)
	server := tmcp.New("stdio-ready", "1.0.0")
	if err := server.Serve(ctx, strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}
	select {
	case <-ready.Done():
	default:
		t.Fatal("Flow readiness was not signaled after MCP streams were initialized")
	}
}

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("", "")
	if got := srv.String(); got != "mcp(nexss-mcp-server)" {
		t.Fatalf("unexpected default name: %s", got)
	}
}

func TestCanHandle(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("test", "1.0.0")
	if srv.CanHandle(nil) {
		t.Fatal("CanHandle must return false for MCP transport")
	}
}

func TestAsAction(t *testing.T) {
	t.Parallel()

	srv := tmcp.New("my-mcp", "1.0.0")
	act := srv.AsAction().Build()
	meta := act.Describe()

	if meta.Name != "transport.mcp.my-mcp" {
		t.Fatalf("unexpected action name: %s", meta.Name)
	}
	if meta.Description == "" {
		t.Fatal("description must not be empty")
	}
}
