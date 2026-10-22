package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/cmd"
)

// cliAPI is a fake API for the lode tool: two projects, the caller alice,
// one task, one document, and a record of every request.
type cliAPI struct {
	mu  sync.Mutex
	got []string // "METHOD path?query body"
}

func (f *cliAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.got = append(f.got, r.Method+" "+r.URL.RequestURI()+" "+string(body))
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer wl_test" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
		io.WriteString(w, `{"projects":[{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]},`+
			`{"id":"other","name":"Other","key":"OT","repos":[],"focus":[]}]}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/whoami":
		io.WriteString(w, `{"id":"alice","kind":"human"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks":
		io.WriteString(w, `{"tasks":[]}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/WL-1":
		io.WriteString(w, `{"id":"WL-1","project":"worklode","title":"The one task"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
		io.WriteString(w, `{"id":"WL-9","project":"worklode","title":"x"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/docs":
		io.WriteString(w, `{"docs":[{"id":7,"project":"worklode","kind":"spec","number":7,"title":"Seven","owner":"bob"}]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"not found"}`)
	}
}

func (f *cliAPI) requests(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.got {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

const cliSecret = "cli-test-secret"

func newCLIServer(t *testing.T) (*server, *cliAPI) {
	t.Helper()
	api := &cliAPI{}
	s := New(Config{
		API: api, PublicURL: "http://lode.test", SessionSecret: cliSecret,
		Metrics: NewMetrics(prometheus.NewRegistry()),
		CLI:     CLI{Run: cmd.InProcess, Command: cmd.ResolveCommand},
	}).(*server)
	return s, api
}

// callLode calls the lode tool through the same dispatcher tools/call uses.
func callLode(t *testing.T, s *server, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req := callReq()
	req.Params = &mcp.CallToolParamsRaw{Name: "lode", Arguments: raw}
	res, err := s.tool("lode", s.lode)(context.Background(), req)
	if err != nil || res == nil {
		t.Fatalf("lode %v: res=%v err=%v", args, res, err)
	}
	return res
}

func block(res *mcp.CallToolResult, i int) string {
	if i >= len(res.Content) {
		return ""
	}
	return res.Content[i].(*mcp.TextContent).Text
}

func wlHandle() string {
	return signHandle(cliSecret, "alice", "WL", time.Now().Add(time.Hour))
}

func TestLodeToolPrintsWhatTheCLIPrints(t *testing.T) {
	s, api := newCLIServer(t)
	res := callLode(t, s, map[string]any{"args": []string{"task", "list", "--json"}, "project": wlHandle()})
	if res.IsError || len(res.Content) != 3 {
		t.Fatalf("result = %+v", res.Content)
	}
	if strings.TrimSpace(block(res, 0)) != `{"tasks":[]}` || block(res, 1) != "" || block(res, 2) != "exit code: 0" {
		t.Errorf("blocks = %q | %q | %q", block(res, 0), block(res, 1), block(res, 2))
	}
	// The handle's project became the command's current project.
	if got := api.requests("GET /api/v1/tasks?project=worklode&"); len(got) != 1 {
		t.Errorf("task list requests = %v, want one scoped to worklode", api.requests("GET /api/v1/tasks"))
	}
}

func TestLodeToolHelp(t *testing.T) {
	s, _ := newCLIServer(t)
	res := callLode(t, s, map[string]any{"args": []string{"task", "--help"}})
	if res.IsError || !strings.Contains(block(res, 0), "Available Commands:") || block(res, 2) != "exit code: 0" {
		t.Fatalf("task --help = %+v", res.Content)
	}
}

func TestLodeToolStdin(t *testing.T) {
	s, api := newCLIServer(t)
	res := callLode(t, s, map[string]any{
		"args":    []string{"task", "add", "--title", "x", "--body-file", "-", "--json"},
		"stdin":   "body from stdin",
		"project": wlHandle(),
	})
	if res.IsError {
		t.Fatalf("task add = %+v", res.Content)
	}
	posts := api.requests("POST /api/v1/tasks")
	if len(posts) != 1 || !strings.Contains(posts[0], "body from stdin") {
		t.Errorf("POST = %v, want the stdin body", posts)
	}

	// A flag naming a path is invalid: the server cannot read the caller's files.
	res = callLode(t, s, map[string]any{
		"args":    []string{"task", "add", "--title", "x", "--body-file", "/etc/hostname"},
		"project": wlHandle(),
	})
	if !res.IsError || !strings.Contains(block(res, 1), "reads only stdin") {
		t.Errorf("path flag = %+v", res.Content)
	}
	assertCounted(t, s, "task add", outcomeInvalid)
}

func TestLodeToolRefusesLocalCommands(t *testing.T) {
	s, api := newCLIServer(t)
	for _, args := range [][]string{{"install"}, {"work", "next"}, {"task", "claim", "WL-1"}} {
		res := callLode(t, s, map[string]any{"args": args, "project": wlHandle()})
		if !res.IsError || !strings.Contains(block(res, 1), "needs the lode binary on the caller's machine") {
			t.Errorf("lode %v = %+v", args, res.Content)
		}
	}
	assertCounted(t, s, "install", outcomeRefused)
	assertCounted(t, s, "work next", outcomeRefused)
	if got := api.requests("POST "); len(got) != 0 {
		t.Errorf("a refused command wrote: %v", got)
	}
}

func TestLodeToolNonZeroExit(t *testing.T) {
	s, _ := newCLIServer(t)
	res := callLode(t, s, map[string]any{"args": []string{"task", "show", "WL-404", "--json"}})
	if !res.IsError || !strings.Contains(block(res, 1), "not found") || block(res, 2) != "exit code: 1" {
		t.Fatalf("missing task = %+v", res.Content)
	}
	assertCounted(t, s, "task show", outcomeNotFound)

	res = callLode(t, s, map[string]any{"args": []string{"bogus"}})
	if !res.IsError || !strings.Contains(block(res, 1), "unknown command") {
		t.Fatalf("bogus = %+v", res.Content)
	}
	assertCounted(t, s, "unknown", outcomeError)
}

func TestLodeToolProject(t *testing.T) {
	s, api := newCLIServer(t)

	// A full ref needs no handle.
	res := callLode(t, s, map[string]any{"args": []string{"task", "show", "WL-1", "--json"}})
	if res.IsError || !strings.Contains(block(res, 0), "The one task") {
		t.Fatalf("task show WL-1 without a handle = %+v", res.Content)
	}

	// A command that needs a project, with no handle.
	res = callLode(t, s, map[string]any{"args": []string{"task", "add", "--title", "x"}})
	if !res.IsError || !strings.Contains(block(res, 0), "project_use") {
		t.Fatalf("no handle = %+v", res.Content)
	}
	if !strings.Contains(block(res, 1), `"candidates"`) || !strings.Contains(block(res, 1), `"OT"`) {
		t.Errorf("project_required detail = %s", block(res, 1))
	}
	assertCounted(t, s, "task add", outcomeProjectRequired)

	// --project naming another project than the handle's.
	res = callLode(t, s, map[string]any{"args": []string{"task", "list", "--project", "OT"}, "project": wlHandle()})
	if !res.IsError || !strings.Contains(block(res, 0), "--project OT") || !strings.Contains(block(res, 1), `"Other"`) {
		t.Fatalf("mismatch = %+v", res.Content)
	}
	assertCounted(t, s, "task list", outcomeConfirmProject)

	// --project with no handle cannot choose one.
	res = callLode(t, s, map[string]any{"args": []string{"task", "list", "--project", "WL"}})
	if !res.IsError {
		t.Fatalf("--project without a handle = %+v", res.Content)
	}

	// A bare key or forged handle is not a handle: nothing runs.
	before := len(api.requests("GET /api/v1/tasks"))
	for _, h := range []string{"WL", signHandle("other-secret", "alice", "WL", time.Now().Add(time.Hour))} {
		res = callLode(t, s, map[string]any{"args": []string{"task", "list"}, "project": h})
		if !res.IsError || !strings.Contains(block(res, 1), `"candidates"`) {
			t.Errorf("handle %q = %+v", h, res.Content)
		}
	}
	if after := len(api.requests("GET /api/v1/tasks")); after != before {
		t.Errorf("a call with an invalid handle ran its command")
	}
	if n := testutil.ToFloat64(s.cfg.Metrics.cliCommands.WithLabelValues("task list", outcomeConfirmProject)); n != 4 {
		t.Errorf("cli{task list, confirm_project} = %v, want 4", n)
	}
}

func TestLodeToolDocTransferConfirmation(t *testing.T) {
	s, api := newCLIServer(t)
	args := []string{"doc", "transfer", "--from", "bob", "--to", "ada"}
	res := callLode(t, s, map[string]any{"args": args, "project": wlHandle()})
	if !res.IsError || !strings.Contains(block(res, 1), `"yes" on stdin`) {
		t.Fatalf("unconfirmed transfer = %+v", res.Content)
	}
	if got := api.requests("POST "); len(got) != 0 {
		t.Fatalf("an unconfirmed transfer wrote: %v", got)
	}
	assertCounted(t, s, "doc transfer", outcomeRefused)

	callLode(t, s, map[string]any{"args": args, "project": wlHandle(), "stdin": "yes"})
	if got := api.requests("POST /api/v1/docs/7/owner"); len(got) != 1 {
		t.Fatalf("confirmed transfer requests = %v", api.requests("POST "))
	}
}

func TestLodeToolMetrics(t *testing.T) {
	s, _ := newCLIServer(t)
	callLode(t, s, map[string]any{"args": []string{"task", "list", "--json"}, "project": wlHandle()})
	callLode(t, s, map[string]any{"args": []string{"task", "list", "--json"}, "project": wlHandle()})
	callLode(t, s, map[string]any{"args": []string{}})
	assertCountedN(t, s, "task list", outcomeOK, 2)
	assertCountedN(t, s, "unknown", outcomeOK, 1)
	if n := testutil.ToFloat64(s.cfg.Metrics.toolCalls.WithLabelValues("lode", outcomeOK)); n != 3 {
		t.Errorf("tool_calls{lode, ok} = %v, want 3", n)
	}

	// Without a wired command tree the tool says so and counts an error.
	bare := New(Config{API: &cliAPI{}, Metrics: NewMetrics(prometheus.NewRegistry())}).(*server)
	res := callLode(t, bare, map[string]any{"args": []string{"task", "list"}})
	if !res.IsError {
		t.Fatalf("unwired = %+v", res.Content)
	}
	assertCounted(t, bare, "unknown", outcomeError)
}

func assertCounted(t *testing.T, s *server, command, outcome string) {
	t.Helper()
	assertCountedN(t, s, command, outcome, 1)
}

func assertCountedN(t *testing.T, s *server, command, outcome string, want float64) {
	t.Helper()
	if n := testutil.ToFloat64(s.cfg.Metrics.cliCommands.WithLabelValues(command, outcome)); n != want {
		t.Errorf("worklode_mcp_cli_commands_total{%q, %q} = %v, want %v", command, outcome, n, want)
	}
	if n := testutil.ToFloat64(s.cfg.Metrics.toolCalls.WithLabelValues("lode", outcome)); n < want {
		t.Errorf("worklode_mcp_tool_calls_total{lode, %q} = %v, want at least %v", outcome, n, want)
	}
}
