package tmcp_test

import (
	"context"
	"io"
	"testing"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/testkit/rpc"
	"github.com/nexssp/transportai/tmcp"
)

type AddReq struct {
	A int `json:"a"`
	B int `json:"b"`
}

type AddRes struct {
	Sum int `json:"sum"`
}

type TextDocRes struct {
	Markdown string
}

func (r TextDocRes) MarshalText() ([]byte, error) {
	return []byte(r.Markdown), nil
}

func newTestServer(t *testing.T, serverName string, actions ...action.AnyAction) *tmcp.Transport {
	t.Helper()

	srv := tmcp.New(serverName, "1.0.0")
	if len(actions) > 0 {
		srv.Mount(actions)
	}
	return srv
}

func newTestClient(t *testing.T, srv *tmcp.Transport) *rpc.Client {
	t.Helper()

	return rpc.DialJSONRPC(t, func(ctx context.Context, in io.Reader, out io.Writer) error {
		return srv.Serve(ctx, in, out)
	})
}

func addAction() action.AnyAction {
	return action.New("math.add", func(_ context.Context, req AddReq) (AddRes, error) {
		return AddRes{Sum: req.A + req.B}, nil
	}).
		Description("Adds two integers").
		Build()
}

func toolText(t *testing.T, resp rpc.Response) string {
	t.Helper()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	resp.BindResult(t, &result)

	if len(result.Content) == 0 {
		t.Fatal("expected content block in tool result")
	}

	return result.Content[0].Text
}
