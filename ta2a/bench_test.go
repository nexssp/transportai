package ta2a_test

import (
	"context"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transportai/ta2a"
)

func echoAction() action.AnyAction {
	return action.New("echo", func(_ context.Context, msg ta2a.Message) (string, error) {
		return msg.Text, nil
	}).Route(ta2a.Role("echo")).Build()
}

func BenchmarkSend_SteadyState(b *testing.B) {
	transport := ta2a.New(":0", nil)
	transport.Mount([]action.AnyAction{echoAction()})

	ctx := context.Background()
	msg := ta2a.Message{
		Role:      "echo",
		Text:      "hello",
		ContextID: "ctx-bench",
	}

	for range 64 {
		_, _ = transport.Send(ctx, msg)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, _ = transport.Send(ctx, msg)
	}
}
