package tmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nexssp/kernel/action"
)

type benchAddReq struct {
	A int `json:"a"`
	B int `json:"b"`
}

type benchAddRes struct {
	Sum int `json:"sum"`
}

func benchServer(tb testing.TB) (*Transport, Request) {
	tb.Helper()

	srv := New("bench", "1.0.0")
	srv.Mount([]action.AnyAction{
		action.New("math.add", func(_ context.Context, req benchAddReq) (benchAddRes, error) {
			return benchAddRes{Sum: req.A + req.B}, nil
		}).Build(),
	})

	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"math.add","arguments":{"a":1,"b":2}}`),
	}

	return srv, req
}

func BenchmarkDispatchToolsCall(b *testing.B) {
	srv, req := benchServer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = srv.dispatch(ctx, req)
	}
}
