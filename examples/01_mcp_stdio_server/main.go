package main

import (
	"context"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transportai/tmcp"
)

type CalcReq struct {
	A int `json:"a" validate:"required"`
	B int `json:"b" validate:"required"`
}

type CalcRes struct {
	Sum     int `json:"sum"`
	Product int `json:"product"`
}

func main() {
	calcAction := action.New("calculator.compute", func(_ context.Context, req CalcReq) (CalcRes, error) {
		return CalcRes{
			Sum:     req.A + req.B,
			Product: req.A * req.B,
		}, nil
	}).
		Description("Calculates sum and product of two numbers").
		Build()

	srv := tmcp.New("nexss-calc-tools", "1.0.0")
	srv.Mount([]action.AnyAction{calcAction})

	// Run MCP over standard input/output (compatible with Claude Desktop & Cursor)
	if err := srv.ServeStdio(context.Background()); err != nil {
		panic(err)
	}
}
