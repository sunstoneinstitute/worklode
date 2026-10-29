//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

type mcpBearerRT string

func (b mcpBearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// mcpDetail decodes a tool result's second content block (WL-RULE-1819).
func mcpDetail(t *testing.T, res *sdk.CallToolResult) map[string]any {
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

// TestMCPProjectTools drives /mcp over HTTP with a minted token: tools list,
// project_use handle and confirm_project paths, project_resolve ranking, and
// the tool-call counter on /metrics (WL-REQ-1822).
func TestMCPProjectTools(t *testing.T) {
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, admin, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		SessionSecret:  "e2e-mcp-secret",
		PublicURL:      "http://lode.test",
		Metrics:        reg,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(main)
	defer srv.Close()

	root := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	for _, p := range []model.CreateProjectInput{
		{ID: "worklode", Name: "Worklode", Key: "WL"},
		{ID: "rollout", Name: "Rollout", Key: "RO"},
	} {
		if _, _, err := root.CreateProject(ctx, p); err != nil {
			t.Fatalf("create project %s: %v", p.Key, err)
		}
	}
	if _, _, err := root.CreateActor(ctx, model.CreateActorInput{ID: "mcp-user", Kind: "human", DisplayName: "MCP User"}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := root.CreateToken(ctx, "mcp-user", "e2e mcp", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// No elicitation handler: the client cannot answer a form.
	c := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil)
	cs, err := c.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerRT(tok.Token)}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	call := func(tool string, args map[string]any) *sdk.CallToolResult {
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return res
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, want := range []string{"project_list", "project_resolve", "project_use"} {
		if !have[want] {
			t.Errorf("tool %s not listed; have %v", want, have)
		}
	}

	res := call("project_use", map[string]any{"project": "WL", "confirmed": true})
	if res.IsError {
		t.Fatalf("confirmed project_use is an error: %+v", res.Content)
	}
	h, _ := mcpDetail(t, res)["project"].(string)
	if !strings.HasPrefix(h, "WL.") {
		t.Fatalf("handle = %q, want WL.<...>", h)
	}

	// The bare key, unconfirmed, on a client without elicitation.
	res = call("project_use", map[string]any{"project": "WL"})
	d := mcpDetail(t, res)
	if _, ok := d["project"]; !res.IsError || ok {
		t.Fatalf("bare key: IsError=%v detail=%v, want confirm_project with no handle", res.IsError, d)
	}
	if cands, _ := d["candidates"].([]any); len(cands) == 0 || cands[0].(map[string]any)["key"] != "WL" {
		t.Fatalf("bare key candidates = %v", d["candidates"])
	}

	res = call("project_resolve", map[string]any{"spoken": "work load"})
	if cands, _ := mcpDetail(t, res)["candidates"].([]any); len(cands) == 0 || cands[0].(map[string]any)["key"] != "WL" {
		t.Fatalf("project_resolve candidates = %v, want WL first", mcpDetail(t, res)["candidates"])
	}

	mcpJourney(t, call, h, tok.Token, srv.URL)

	body := scrapeMetrics(t, admin)
	for _, want := range []string{
		`worklode_mcp_tool_calls_total{outcome="confirm_project",tool="project_use"} 1`,
		`worklode_mcp_tool_calls_total{outcome="ok",tool="file"} 2`,
		`worklode_mcp_tool_calls_total{outcome="ok",tool="decision_resolve"} 2`,
		`worklode_mcp_tool_calls_total{outcome="ok",tool="approval_decide"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %s", want)
		}
	}
}

// mcpJourney files a bug, finds it, and walks a decision and an approval
// through the unconfirmed fallback, which records nothing, then the confirmed
// call, which records the user's answer (WL-REQ-1842, WL-REQ-1843).
func mcpJourney(t *testing.T, call func(string, map[string]any) *sdk.CallToolResult, h, token, url string) {
	t.Helper()
	ctx := context.Background()
	api := cli.NewClient(cli.Config{ServerURL: url, Token: token})
	root := cli.NewClient(cli.Config{ServerURL: url, Token: bootstrapToken})
	// Recording an answer assigns the task, so the decider is on the crew.
	if _, _, err := root.AddCrewMember(ctx, "worklode", "mcp-user", "", false, false); err != nil {
		t.Fatalf("add crew: %v", err)
	}
	first := func(res *sdk.CallToolResult) string { return res.Content[0].(*sdk.TextContent).Text }
	ok := func(tool string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		res := call(tool, args)
		if res.IsError {
			t.Fatalf("%s = %s", tool, first(res))
		}
		return res
	}

	out := first(ok("file", map[string]any{"project": h, "what": "bug", "title": "Crash on save", "body": "It crashes."}))
	bug := strings.TrimRight(strings.Fields(out)[1], ":,")
	if !strings.HasPrefix(bug, "WL-") {
		t.Fatalf("file bug = %q", out)
	}
	if res := ok("task_list", map[string]any{"project": h, "kind": "bugs"}); !strings.Contains(res.Content[1].(*sdk.TextContent).Text, bug) {
		t.Fatalf("task_list bugs does not show %s: %+v", bug, res.Content)
	}
	spoken := "task " + strings.TrimPrefix(bug, "WL-")
	if out := first(ok("show", map[string]any{"project": h, "ref": spoken})); !strings.Contains(out, "Crash on save") {
		t.Fatalf("show %q = %q", spoken, out)
	}

	out = first(ok("decision_add", map[string]any{"project": h, "task": bug, "question": "Which database?", "options": []string{"Postgres", "SQLite"}}))
	key := strings.TrimSuffix(strings.TrimPrefix(strings.Fields(out)[1], bug+"/"), ":")
	answered := func() bool {
		t.Helper()
		d, _, err := api.GetDecision(ctx, bug, key)
		if err != nil {
			t.Fatalf("get decision %s: %v", key, err)
		}
		return d.Answer != nil
	}
	ok("decision_resolve", map[string]any{"project": h, "task": bug, "key": key, "answer": "Postgres"})
	if answered() {
		t.Fatal("decision_resolve without confirmed recorded an answer")
	}
	ok("decision_resolve", map[string]any{"project": h, "task": bug, "key": key, "answer": "Postgres", "confirmed": true})
	if !answered() {
		t.Fatal("confirmed decision_resolve recorded nothing")
	}

	// The approver is someone else: an author cannot decide their own change.
	if _, _, err := root.CreateActor(ctx, model.CreateActorInput{ID: "mcp-reviewer", Kind: "human", DisplayName: "MCP Reviewer"}); err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	rtok, _, err := root.CreateToken(ctx, "mcp-reviewer", "e2e mcp", nil)
	if err != nil {
		t.Fatalf("create reviewer token: %v", err)
	}
	rc, err := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: mcpBearerRT(rtok.Token)}, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect reviewer: %v", err)
	}
	defer rc.Close()
	reviewer := func(args map[string]any) {
		t.Helper()
		res, err := rc.CallTool(ctx, &sdk.CallToolParams{Name: "approval_decide", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("approval_decide = %v %+v", err, res)
		}
	}

	// On a doc: a task approval carries no revision and cannot be decided.
	out = first(ok("file", map[string]any{"project": h, "what": "spec", "title": "Reviewed thing", "body": "## 1. Scope {#sec-1}\n\nText.\n"}))
	spec := strings.TrimSuffix(strings.Fields(out)[4], ":")
	out = first(ok("approval_add", map[string]any{"project": h, "entity": spec, "approver": "mcp-reviewer"}))
	id, err := strconv.ParseInt(strings.Fields(out)[1], 10, 64)
	if err != nil {
		t.Fatalf("approval_add = %q: %v", out, err)
	}
	awaiting := func() bool {
		t.Helper()
		r, _, err := api.ListApprovals(ctx, model.ApprovalListParams{})
		if err != nil {
			t.Fatalf("list approvals: %v", err)
		}
		for _, a := range r.Approvals {
			if a.ID == id {
				return a.State == "awaiting"
			}
		}
		return false
	}
	reviewer(map[string]any{"id": id, "verdict": "approve"})
	if !awaiting() {
		t.Fatal("approval_decide without confirmed decided the approval")
	}
	reviewer(map[string]any{"id": id, "verdict": "approve", "confirmed": true})
	if awaiting() {
		t.Fatal("confirmed approval_decide left the approval awaiting")
	}
}
