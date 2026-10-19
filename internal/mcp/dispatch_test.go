package mcp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func callReq() *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: http.Header{"Authorization": {"Bearer wl_test"}}}}
}

func TestStatusOutcome(t *testing.T) {
	for status, want := range map[int]string{
		200: "ok", 204: "ok", 400: "invalid", 422: "invalid", 401: "refused", 403: "refused",
		404: "not_found", 409: "conflict", 500: "error", 502: "error", 429: "error",
	} {
		if got := statusOutcome(status); got != want {
			t.Errorf("statusOutcome(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestToolOutcomesAndPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &server{cfg: Config{API: stubAPI, Metrics: NewMetrics(reg)}}
	ctx := context.Background()
	ok := &mcp.CallToolResult{}
	cases := []struct {
		name    string
		fn      toolFunc
		outcome string
		isErr   bool
	}{
		{"ok", func(context.Context, *cli.Client, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return ok, nil }, "ok", false},
		{"invalid", failWith(&cli.ClientError{Status: 422, Msg: "bad"}), "invalid", true},
		{"bad_cursor", failWith(errInvalidCursor), "invalid", true},
		{"refused", failWith(&cli.ClientError{Status: 403, Msg: "no"}), "refused", true},
		{"not_found", failWith(&cli.ClientError{Status: 404, Msg: "gone"}), "not_found", true},
		{"conflict", failWith(&cli.ClientError{Status: 409, Msg: "dup"}), "conflict", true},
		{"error", failWith(errors.New("boom")), "error", true},
		{"project_required", failWith(&toolError{outcomeProjectRequired, "pick one"}), "project_required", true},
		{"confirm_project", failWith(&toolError{outcomeConfirmProject, "sure?"}), "confirm_project", true},
		{"panic", func(context.Context, *cli.Client, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { panic("x") }, "error", true},
	}
	for _, c := range cases {
		res, err := s.tool(c.name, c.fn)(ctx, callReq())
		if err != nil || res == nil || res.IsError != c.isErr {
			t.Fatalf("%s: res=%+v err=%v", c.name, res, err)
		}
		if n := testutil.ToFloat64(s.cfg.Metrics.toolCalls.WithLabelValues(c.name, c.outcome)); n != 1 {
			t.Errorf("%s: counter{%s} = %v, want 1", c.name, c.outcome, n)
		}
	}
	// The server keeps serving after the panic.
	if res, err := s.tool("ok", cases[0].fn)(ctx, callReq()); err != nil || res.IsError {
		t.Fatalf("call after panic: %+v %v", res, err)
	}
}

func TestNilMetricsIsSafe(t *testing.T) {
	s := &server{cfg: Config{API: stubAPI}}
	res, err := s.tool("x", func(context.Context, *cli.Client, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		panic("x")
	})(context.Background(), callReq())
	if err != nil || !res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func failWith(err error) toolFunc {
	return func(context.Context, *cli.Client, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, err }
}
