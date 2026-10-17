package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// A tool that panics answers an error result and counts as outcome error;
// the SDK runs tools on bare goroutines, so an unrecovered panic would end
// the process and this test with it.
func TestMCPToolPanicIsAnErrorResult(t *testing.T) {
	t.Parallel()
	metrics, err := newMCPMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	s := &server{log: slog.New(slog.NewTextHandler(io.Discard, nil)), api: http.NotFoundHandler(), metrics: metrics}
	srv := sdk.NewServer(&sdk.Implementation{Name: "t", Version: "1"}, nil)
	addMCPTool(s, srv, "show", "Show", "Panics.", true,
		func(*mcpCall, model.MCPShowInput) (string, model.MCPShowOutput, error) { panic("boom") })

	ct, st := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "show", Arguments: map[string]any{"ref": "x"}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("a panicking tool did not answer an error result")
	}
	if got := testutil.ToFloat64(s.metrics.calls.WithLabelValues("show", "error")); got != 1 {
		t.Fatalf("show error calls = %v; want 1", got)
	}
}

// A /api/v1 handler that panics under the loopback answers 500 instead of
// unwinding the tool's goroutine.
func TestMCPLoopbackPanicIs500(t *testing.T) {
	t.Parallel()
	l := mcpLoopback{h: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Half", "written")
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resp, err := l.RoundTrip(httptest.NewRequest(http.MethodGet, "http://mcp.loopback/api/v1/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError || strings.Contains(string(body), "partial") {
		t.Fatalf("loopback panic = %d %q; want a clean 500", resp.StatusCode, body)
	}
}

// show classifies a refused or missing rule fetch as the API's answer, and
// only a document failure as invalid.
func TestShowTextErr(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("fetch WL-REQ-1: %w", &cli.ClientError{Status: http.StatusForbidden, Msg: "forbidden"}), "refused"},
		{fmt.Errorf("fetch WL-REQ-1: %w", &cli.ClientError{Status: http.StatusNotFound, Msg: "no rule"}), "not_found"},
		{fmt.Errorf("no section sec-9 in WL-SPEC-1"), "invalid"},
	} {
		if got, _ := mcpErrorText(showTextErr(c.err)); got != c.want {
			t.Errorf("showTextErr(%v) outcome = %q; want %q", c.err, got, c.want)
		}
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		h, want string
		ok      bool
	}{
		{"Bearer wl_x", "wl_x", true},
		{"bearer wl_x", "wl_x", true},
		{"BEARER  wl_x", "wl_x", true},
		{"Basic wl_x", "", false},
		{"Bearer ", "", false},
		{"Bearerwl_x", "", false},
		{"", "", false},
	} {
		got, ok := bearerToken(http.Header{"Authorization": {c.h}})
		if got != c.want || ok != c.ok {
			t.Errorf("bearerToken(%q) = %q %v; want %q %v", c.h, got, ok, c.want, c.ok)
		}
	}
}
