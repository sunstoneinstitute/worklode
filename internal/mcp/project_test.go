// External test package: internal/api imports internal/mcp, so a test over the
// real API mux cannot live in package mcp.
package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	wlmcp "github.com/sunstoneinstitute/worklode/internal/mcp"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const testSecret = "mcp-test-secret"

type fixture struct {
	url        string
	st         *store.Store
	api        http.Handler
	reg        *prometheus.Registry
	alice, bob string
}

// newFixture serves /mcp over the real API mux and a test store, with the
// scoped_probe tool added.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	f := &fixture{reg: prometheus.NewRegistry(), st: st}
	for _, who := range []string{"alice", "bob"} {
		if err := st.CreateActor(ctx, who, "human", who, false); err != nil {
			t.Fatal(err)
		}
		tok, err := st.CreateToken(ctx, who, "test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if who == "alice" {
			f.alice = tok
		} else {
			f.bob = tok
		}
	}
	for _, p := range [][3]string{{"worklode", "Worklode", "WL"}, {"rollout", "Rollout", "RO"}, {"payments", "Payments", "PAY"}} {
		if err := st.CreateProject(ctx, p[0], p[1], p[2]); err != nil {
			t.Fatal(err)
		}
	}
	apiH, _, err := api.NewServer(st, api.Config{SessionSecret: testSecret, BackgroundCtx: t.Context(), EventPoll: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	f.api = apiH
	h := wlmcp.New(wlmcp.Config{API: apiH, PublicURL: "http://lode.test", SessionSecret: testSecret, Metrics: wlmcp.NewMetrics(f.reg)})
	wlmcp.AddScopedProbe(h)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

type bearerRT string

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// connect opens a client session on protocol version, with an elicitation
// handler when elicit is non-nil.
func (f *fixture) connect(t *testing.T, token, version string, elicit func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error)) *sdk.ClientSession {
	t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, &sdk.ClientOptions{ElicitationHandler: elicit})
	cs, err := c.Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint: f.url, HTTPClient: &http.Client{Transport: bearerRT(token)}, DisableStandaloneSSE: true,
	}, &sdk.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connect %s: %v", version, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return res
}

// detail decodes the result's second content block.
func detail(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	if len(res.Content) < 2 {
		t.Fatalf("result has no detail block: %+v", res.Content)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Content[1].(*sdk.TextContent).Text), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func handleOf(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if res.IsError {
		t.Fatalf("want a handle, got error result: %s", res.Content[0].(*sdk.TextContent).Text)
	}
	h, _ := detail(t, res)["project"].(string)
	if !strings.HasPrefix(h, "WL.") {
		t.Fatalf("handle = %q", h)
	}
	return h
}

// assertConfirm checks a confirmation answer: no handle, the candidates, the
// WL-REQ-1829 instruction and the from_instructions shortcut.
func assertConfirm(t *testing.T, res *sdk.CallToolResult, wantKey string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("want a confirmation answer, got %+v", res.Content)
	}
	d := detail(t, res)
	if _, ok := d["project"]; ok {
		t.Fatalf("confirmation answer carries a handle: %v", d)
	}
	if d["instruction"] != "Confirm the project with the user before calling project_use. In a text or graphical interface, ask with your question tool (e.g. AskUserQuestion) offering these projects as options. In a voice conversation, read back the full project name and wait for the user to say yes." {
		t.Fatalf("instruction = %q", d["instruction"])
	}
	if s, _ := d["shortcut"].(string); !strings.Contains(s, "from_instructions=true") || !strings.Contains(s, "Worklode project: <name>") {
		t.Fatalf("shortcut = %q", s)
	}
	cands, _ := d["candidates"].([]any)
	if wantKey != "" && (len(cands) == 0 || cands[0].(map[string]any)["key"] != wantKey) {
		t.Fatalf("candidates = %v, want %s first", cands, wantKey)
	}
}

func (f *fixture) count(t *testing.T, tool, outcome string) float64 {
	t.Helper()
	mfs, err := f.reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "worklode_mcp_tool_calls_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["tool"] == tool && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

const (
	oldProtocol = "2025-11-25"
	newProtocol = "2026-07-28"
)

func TestProjectUsePaths(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)

	// from_instructions and confirmed return the handle.
	handleOf(t, call(t, cs, "project_use", map[string]any{"project": "worklode", "from_instructions": true}))
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))

	// Neither, on a client without elicitation: confirm_project with the project.
	assertConfirm(t, call(t, cs, "project_use", map[string]any{"project": "WL"}), "WL")

	// A near-miss name is confirm_project even when confirmed.
	assertConfirm(t, call(t, cs, "project_use", map[string]any{"project": "work load", "confirmed": true}), "WL")
	assertConfirm(t, call(t, cs, "project_use", map[string]any{"project": "Worklod", "from_instructions": true}), "WL")

	// The handle opens a project-scoped tool.
	res := call(t, cs, "scoped_probe", map[string]any{"project": h})
	if res.IsError || res.Content[0].(*sdk.TextContent).Text != "project WL" {
		t.Fatalf("probe with handle = %+v", res.Content)
	}
	if n := f.count(t, "project_use", "confirm_project"); n != 3 {
		t.Fatalf("project_use confirm_project = %v, want 3", n)
	}
	if n := f.count(t, "project_use", "ok"); n != 2 {
		t.Fatalf("project_use ok = %v, want 2", n)
	}
}

func TestRequireProject(t *testing.T) {
	f := newFixture(t)
	alice := f.connect(t, f.alice, oldProtocol, nil)
	bob := f.connect(t, f.bob, oldProtocol, nil)
	bobs := handleOf(t, call(t, bob, "project_use", map[string]any{"project": "WL", "confirmed": true}))

	// A handle signed for another actor, a bare key and a tampered handle.
	assertConfirm(t, call(t, alice, "scoped_probe", map[string]any{"project": bobs}), "WL")
	assertConfirm(t, call(t, alice, "scoped_probe", map[string]any{"project": "WL"}), "WL")
	assertConfirm(t, call(t, alice, "scoped_probe", map[string]any{"project": "WL.AAAAAAAAAAAAAAAA"}), "WL")

	// Nothing: project_required with the project list.
	res := call(t, alice, "scoped_probe", nil)
	assertConfirm(t, res, "")
	if cands := detail(t, res)["candidates"].([]any); len(cands) != 3 {
		t.Fatalf("project_required list = %v, want 3 projects", cands)
	}

	// A full ref bypasses the handle for that one item.
	res = call(t, alice, "scoped_probe", map[string]any{"ref": "RO-12"})
	if res.IsError || res.Content[0].(*sdk.TextContent).Text != "project RO" {
		t.Fatalf("probe with full ref = %+v", res.Content)
	}

	if n := f.count(t, "scoped_probe", "confirm_project"); n != 3 {
		t.Fatalf("confirm_project = %v, want 3", n)
	}
	if n := f.count(t, "scoped_probe", "project_required"); n != 1 {
		t.Fatalf("project_required = %v, want 1", n)
	}
}

func TestProjectUseElicitation(t *testing.T) {
	f := newFixture(t)
	var asked []string
	accept := func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		asked = append(asked, req.Params.Message)
		return &sdk.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
	}
	decline := func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		return &sdk.ElicitResult{Action: "decline"}, nil
	}

	// 2026-07-28 with form elicitation: the accept returns the handle on the retry.
	handleOf(t, call(t, f.connect(t, f.alice, newProtocol, accept), "project_use", map[string]any{"project": "WL"}))
	if len(asked) != 1 || asked[0] != "Use the Worklode project Worklode (WL) for this conversation?" {
		t.Fatalf("elicitation = %q", asked)
	}
	// A decline returns no handle.
	assertConfirm(t, call(t, f.connect(t, f.alice, newProtocol, decline), "project_use", map[string]any{"project": "WL"}), "WL")
	// 2026-07-28 without elicitation, and an older protocol with it: no elicitation.
	assertConfirm(t, call(t, f.connect(t, f.alice, newProtocol, nil), "project_use", map[string]any{"project": "WL"}), "WL")
	assertConfirm(t, call(t, f.connect(t, f.alice, oldProtocol, accept), "project_use", map[string]any{"project": "WL"}), "WL")
	if len(asked) != 1 {
		t.Fatalf("elicited %d times, want 1", len(asked))
	}

	// Without the retry loop the raw answer is input_required with no content.
	c := sdk.NewClient(&sdk.Implementation{Name: "raw", Version: "0"}, &sdk.ClientOptions{ElicitationHandler: accept, MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true}})
	cs, err := c.Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint: f.url, HTTPClient: &http.Client{Transport: bearerRT(f.alice)}, DisableStandaloneSSE: true,
	}, &sdk.ClientSessionOptions{ProtocolVersion: newProtocol})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res := call(t, cs, "project_use", map[string]any{"project": "WL"})
	if !res.NeedsInput() || res.InputRequests["confirm"] == nil || len(res.Content) != 0 {
		t.Fatalf("raw result = %+v", res)
	}
}

func TestProjectResolveAndList(t *testing.T) {
	f := newFixture(t)
	cs := f.connect(t, f.alice, oldProtocol, nil)

	res := call(t, cs, "project_resolve", map[string]any{"spoken": "work load"})
	if res.IsError {
		t.Fatalf("project_resolve = %+v", res.Content)
	}
	d := detail(t, res)
	if _, ok := d["project"]; ok {
		t.Fatal("project_resolve selected a project")
	}
	cands := d["candidates"].([]any)
	if top := cands[0].(map[string]any); top["key"] != "WL" || top["name"] != "Worklode" || top["confidence"] == nil {
		t.Fatalf("candidates = %v", cands)
	}
	if d["instruction"] == nil || d["shortcut"] == nil {
		t.Fatalf("project_resolve answer lacks the instruction: %v", d)
	}

	res = call(t, cs, "project_list", nil)
	if got := res.Content[0].(*sdk.TextContent).Text; got != "Worklode has 3 projects." {
		t.Fatalf("project_list sentence = %q", got)
	}

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if strings.HasPrefix(tl.Name, "project_") && !strings.Contains(tl.Description, "Worklode project: <name>") {
			t.Errorf("%s description lacks the Worklode project: <name> sentence", tl.Name)
		}
	}
}
