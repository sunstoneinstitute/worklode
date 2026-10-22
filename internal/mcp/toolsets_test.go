package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// rawPost posts one JSON-RPC request with the Worklode-Tools header set to
// tools (not set when empty).
func rawPost(h http.Handler, tools, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer wl_test")
	if tools != "" {
		req.Header.Set(toolsHeader, tools)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// rpcWithTools returns the status and decoded JSON-RPC body of rawPost.
func rpcWithTools(t *testing.T, h http.Handler, tools, body string) (int, map[string]any) {
	t.Helper()
	rr := rawPost(h, tools, body)
	raw := rr.Body.String()
	if i := strings.Index(raw, "data: "); i >= 0 {
		raw = raw[i+len("data: "):]
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		t.Fatalf("%d %q: %v", rr.Code, rr.Body, err)
	}
	return rr.Code, out
}

func listedTools(t *testing.T, h http.Handler, tools string) []string {
	t.Helper()
	code, out := rpcWithTools(t, h, tools, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code != http.StatusOK {
		t.Fatalf("tools/list with %q = %d %v", tools, code, out)
	}
	var names []string
	for _, tl := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	slices.Sort(names)
	return names
}

func TestToolSetsPerRequestHeader(t *testing.T) {
	s, _ := newCLIServer(t)

	want := slices.Clone(wantTools)
	slices.Sort(want)
	if got := listedTools(t, s, ""); !slices.Equal(got, want) {
		t.Errorf("no header lists %v, want %v", got, want)
	}
	if got, want := listedTools(t, s, "cli"), []string{"lode", "project_list", "project_resolve", "project_use"}; !slices.Equal(got, want) {
		t.Errorf("cli lists %v, want %v", got, want)
	}
	// The header is read per request: the next request without it is full again.
	if got := listedTools(t, s, ""); !slices.Equal(got, want) {
		t.Errorf("after a cli request, no header lists %v, want %v", got, want)
	}
}

func TestBadToolsHeaderAnswersInvalidOnEveryRequest(t *testing.T) {
	s, _ := newCLIServer(t)
	for _, v := range []string{"CLI", "full", "cli,full", " "} {
		for _, body := range []string{
			`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}`,
			`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`,
			`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"lode","arguments":{"args":["task","list"]}}}`,
		} {
			rr := rawPost(s, v, body)
			var resp struct {
				ID    int
				Error struct{ Code int }
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || rr.Code != http.StatusBadRequest || resp.ID != 7 || resp.Error.Code != -32600 {
				t.Errorf("header %q, %s = %d %s", v, body, rr.Code, rr.Body)
			}
		}
	}
	if n := testutil.ToFloat64(s.cfg.Metrics.toolCalls.WithLabelValues("lode", outcomeInvalid)); n != 4 {
		t.Errorf("tool_calls{lode, invalid} = %v, want 4", n)
	}
}

func TestShowUnderCLISetIsInvalid(t *testing.T) {
	s, _ := newCLIServer(t)
	code, out := rpcWithTools(t, s, "cli", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"show","arguments":{"ref":"WL-1"}}}`)
	res, _ := out["result"].(map[string]any)
	if code != http.StatusOK || res == nil || res["isError"] != true {
		t.Fatalf("show under cli = %d %v, want an error result", code, out)
	}
	if n := testutil.ToFloat64(s.cfg.Metrics.toolCalls.WithLabelValues("show", outcomeInvalid)); n != 1 {
		t.Errorf("tool_calls{show, invalid} = %v, want 1", n)
	}
	// A name no tool has counts under a bounded label.
	rpcWithTools(t, s, "cli", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if n := testutil.ToFloat64(s.cfg.Metrics.toolCalls.WithLabelValues("unknown", outcomeInvalid)); n != 1 {
		t.Errorf("tool_calls{unknown, invalid} = %v, want 1", n)
	}
}

func TestLodeUnderCLISetWorks(t *testing.T) {
	s, _ := newCLIServer(t)
	code, out := rpcWithTools(t, s, "cli", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"lode","arguments":{"args":["task","list","--json"],"project":"`+wlHandle()+`"}}}`)
	res, _ := out["result"].(map[string]any)
	if code != http.StatusOK || res == nil || res["isError"] == true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), `"tasks"`) {
		t.Fatalf("lode under cli = %d %v", code, out)
	}
}
