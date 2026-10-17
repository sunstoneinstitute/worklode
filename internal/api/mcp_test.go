package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// mcpFixture is a server with two projects, Worklode (WL) and Data Platform
// (DP), an admin token, and the admin handler for reading metrics.
type mcpFixture struct {
	st    *store.Store
	h     http.Handler
	admin http.Handler
	srv   *httptest.Server
	token string
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	st := newTestStore(t)
	token := seedActor(t, st, "alice", "human", "Alice", true)
	ctx := context.Background()
	if err := st.CreateProject(ctx, "worklode", "Worklode", "WL"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProject(ctx, "data", "Data Platform", "DP"); err != nil {
		t.Fatal(err)
	}
	h, admin, err := api.NewServer(st, api.Config{WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &mcpFixture{st: st, h: h, admin: admin, srv: srv, token: token}
}

// bearerRT adds a bearer token to every request.
type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connect opens an MCP session to f's /mcp with token.
func (f *mcpFixture) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   f.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{token}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// callTool calls name and returns its text and structured result.
func callTool(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (text string, structured map[string]any, isError bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &structured)
	}
	return text, structured, res.IsError
}

// mustCall is callTool failing the test on an error result.
func mustCall(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, map[string]any) {
	t.Helper()
	text, out, isErr := callTool(t, sess, name, args)
	if isErr {
		t.Fatalf("%s(%v) failed: %s", name, args, text)
	}
	return text, out
}

func TestMCPToolsList(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	if got := sess.InitializeResult().ServerInfo.Name; got != "worklode" {
		t.Errorf("server name = %q, want worklode", got)
	}
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
	}
	want := []string{"doc_add", "doc_edit", "doc_list", "doc_transition", "file", "project_list",
		"project_resolve", "project_use", "rule_add", "rule_edit", "rule_link", "rule_list",
		"rule_supersede", "search", "show", "task_add", "task_list", "task_set_state", "task_update"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want the 19 %v", names, want)
	}
}

func TestMCPUnauthorized(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	for _, token := range []string{"", "wl_0000000000000000000000000000000000000000"} {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/mcp", strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", token, resp.StatusCode)
		}
		// No OIDC: no login to point at, so the challenge is bare.
		if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("token %q: WWW-Authenticate = %q, want Bearer", token, got)
		}
	}

	_, h, _ := newOIDCServer(t, api.Config{MCPPublicURL: "https://mcp.example.com"})
	for host, want := range map[string]string{
		"localhost:8080":  `Bearer resource_metadata="http://localhost:8080/.well-known/oauth-protected-resource/mcp"`,
		"mcp.example.com": `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`,
		"evil.example":    "Bearer",
	} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		req.Host = host
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", host, rr.Code)
		}
		if got := rr.Header().Get("WWW-Authenticate"); got != want {
			t.Errorf("%s: WWW-Authenticate = %q, want %q", host, got, want)
		}
	}
}

func TestMCPProtectedResourceMetadata(t *testing.T) {
	t.Parallel()
	_, h, _ := newOIDCServer(t, api.Config{MCPPublicURL: "https://mcp.example.com/"})
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		for host, origin := range map[string]string{
			"localhost:8080":  "http://localhost:8080",
			"MCP.example.com": "https://mcp.example.com",
		} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = host
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("%s on %s: status %d %s", path, host, rr.Code, rr.Body.String())
			}
			var md model.ProtectedResourceMetadata
			decodeInto(t, rr, &md)
			if md.Resource != origin+"/mcp" || !slices.Equal(md.AuthorizationServers, []string{origin}) ||
				!slices.Equal(md.BearerMethodsSupported, []string{"header"}) {
				t.Errorf("%s on %s: metadata = %+v, want resource %s/mcp served by %s", path, host, md, origin, origin)
			}
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "evil.example"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s on an unconfigured host: status %d, want 404", path, rr.Code)
		}
	}

	// No OIDC, no login: no metadata, while /mcp still serves a bearer token.
	f := newMCPFixture(t)
	resp, err := http.Get(f.srv.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("metadata without OIDC: status %d, want 404", resp.StatusCode)
	}
}

func TestMCPProjectRequiredThenUse(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)

	text, _, isErr := callTool(t, sess, "task_list", nil)
	if !isErr {
		t.Fatalf("task_list with no project succeeded: %s", text)
	}
	for _, want := range []string{"project_required",
		"Ask the user which Worklode project to use for this session, then call project_use.",
		"Worklode (WL)", "Data Platform (DP)"} {
		if !strings.Contains(text, want) {
			t.Errorf("project_required text lacks %q: %s", want, text)
		}
	}

	text, out := mustCall(t, sess, "project_use", map[string]any{"project": "wl"})
	if !strings.HasPrefix(text, "This session now works in the Worklode project Worklode (WL).") {
		t.Errorf("project_use text = %q", text)
	}
	if p, _ := out["project"].(map[string]any); p["id"] != "worklode" {
		t.Errorf("project_use output = %v", out)
	}
	text, _ = mustCall(t, sess, "task_list", nil)
	if !strings.HasPrefix(text, "Worklode has 0 open tasks.") {
		t.Errorf("task_list text = %q", text)
	}

	// The default is per session: a second session starts with none.
	other := f.connect(t, f.token)
	if _, _, isErr := callTool(t, other, "task_list", nil); !isErr {
		t.Error("a new session inherited another session's project")
	}
	// An explicit argument wins, by key or spoken name.
	mustCall(t, other, "task_list", map[string]any{"project": "data platform"})
}

func TestMCPProjectResolve(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	text, out := mustCall(t, sess, "project_resolve", map[string]any{"spoken": "work load"})
	cands, _ := out["candidates"].([]any)
	if len(cands) == 0 || out["clear"] != true {
		t.Fatalf("project_resolve(work load) = %v", out)
	}
	if first, _ := cands[0].(map[string]any); first["key"] != "WL" {
		t.Errorf("first candidate = %v, want WL", first)
	}
	if !strings.HasPrefix(text, `"work load" is the Worklode project Worklode (WL).`) {
		t.Errorf("text = %q", text)
	}
	_, out = mustCall(t, sess, "project_resolve", map[string]any{"spoken": "the data platform project"})
	if first, _ := out["candidates"].([]any)[0].(map[string]any); first["key"] != "DP" {
		t.Errorf("first candidate for data platform = %v", first)
	}
	text, out = mustCall(t, sess, "project_resolve", map[string]any{"spoken": "zebra"})
	if c, _ := out["candidates"].([]any); len(c) != 0 || !strings.HasPrefix(text, `No Worklode project sounds like "zebra".`) {
		t.Errorf("project_resolve(zebra) = %q %v", text, out)
	}
}

func TestMCPFileBug(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	mustCall(t, sess, "project_use", map[string]any{"project": "WL"})

	text, out := mustCall(t, sess, "file", map[string]any{"what": "Bug", "title": "Fix login redirect", "body": "It loops."})
	if out["entity"] != "task" || out["kind"] != "bug" {
		t.Fatalf("file(bug) output = %v", out)
	}
	ref, _ := out["ref"].(string)
	if want := "Created bug task " + ref + `, "Fix login redirect", in Worklode.`; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	rr := doReq(t, f.h, http.MethodGet, "/api/v1/tasks/"+ref, f.token, nil)
	var task model.TaskDetail
	decodeInto(t, rr, &task)
	if task.Kind != "bug" || task.Project != "worklode" || task.Body != "It loops." {
		t.Errorf("filed task = %+v", task.Task)
	}

	// The list filter takes the same words, plural included.
	text, out = mustCall(t, sess, "task_list", map[string]any{"kind": "bugs"})
	if tasks, _ := out["tasks"].([]any); len(tasks) != 1 || !strings.HasPrefix(text, "Worklode has 1 open bug task.") {
		t.Errorf("task_list(bugs) = %q %v", text, out)
	}

	// Spoken task refs resolve in the session's project.
	n := strings.TrimPrefix(ref, "WL-")
	text, _ = mustCall(t, sess, "task_set_state", map[string]any{"id": n, "state": "abandoned"})
	if !strings.Contains(text, "to abandoned") {
		t.Errorf("task_set_state text = %q", text)
	}

	text, _, isErr := callTool(t, sess, "file", map[string]any{"what": "chores", "title": "x"})
	if isErr || !strings.HasPrefix(text, "Created chore task") {
		t.Errorf("file(chores) = %q", text)
	}
	text, _, isErr = callTool(t, sess, "file", map[string]any{"what": "bugg", "title": "x"})
	if !isErr || !strings.Contains(text, `no kind called "bugg"`) || !strings.Contains(text, "bug (a bug task)") {
		t.Errorf("file(bugg) = %q", text)
	}
	text, _, isErr = callTool(t, sess, "file", map[string]any{"what": "design", "title": "x"})
	if !isErr || !strings.Contains(text, "design task or a spec") {
		t.Errorf("file(design) = %q", text)
	}
}

func TestMCPDocRoundTrip(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	scope := func(m map[string]any) map[string]any { m["project"] = "WL"; return m }

	text, out := mustCall(t, sess, "doc_add", scope(map[string]any{
		"kind": "spec", "title": "Login flow", "body": "## 1. Scope {#sec-1}\n\nThe first text.\n",
	}))
	doc, _ := out["doc"].(map[string]any)
	ref, _ := doc["ref"].(string)
	if ref != "WL-SPEC-1" || doc["title"] != "Login flow" || doc["status"] != "draft" {
		t.Fatalf("doc_add output = %v", out)
	}
	if !strings.HasPrefix(text, `Created draft spec WL-SPEC-1, "Login flow", in Worklode.`) {
		t.Errorf("doc_add text = %q", text)
	}
	version := int(doc["version"].(float64))

	text, _, isErr := callTool(t, sess, "doc_edit", scope(map[string]any{
		"id": "spec one", "body": "# Login flow\n\n## 1. Scope {#sec-1}\n\nStale.\n", "if_version": version + 5,
	}))
	if !isErr || !strings.Contains(text, "conflicts with the current state") {
		t.Errorf("doc_edit with a stale if_version = %q", text)
	}
	text, out = mustCall(t, sess, "doc_edit", scope(map[string]any{
		"id": "spec one", "body": "# Login flow\n\n## 1. Scope {#sec-1}\n\nThe second text.\n", "if_version": version,
	}))
	if d, _ := out["doc"].(map[string]any); int(d["version"].(float64)) <= version {
		t.Errorf("doc_edit did not move the version: %v", out)
	}
	if !strings.HasPrefix(text, "Rewrote WL-SPEC-1") {
		t.Errorf("doc_edit text = %q", text)
	}

	mustCall(t, sess, "doc_transition", scope(map[string]any{"id": "WL-SPEC-1", "action": "submit"}))
	text, out = mustCall(t, sess, "doc_transition", scope(map[string]any{"id": "wl-spec-1", "action": "accept"}))
	if d, _ := out["doc"].(map[string]any); d["status"] != "accepted" || !strings.HasPrefix(text, "Accepted WL-SPEC-1") {
		t.Errorf("accept = %q %v", text, out)
	}

	text, out = mustCall(t, sess, "show", scope(map[string]any{"ref": "spec one", "section": "1"}))
	if out["ref"] != "WL-SPEC-1" || !strings.Contains(text, "The second text.") ||
		!strings.HasPrefix(text, `Here is section 1 of spec WL-SPEC-1, "Login flow" (accepted`) {
		t.Errorf("show inline = %q %v", text, out)
	}
	text, _ = mustCall(t, sess, "show", scope(map[string]any{"ref": "WL-SPEC-1", "inline": false}))
	if !strings.Contains(text, "The second text.") {
		t.Errorf("show raw = %q", text)
	}

	text, out = mustCall(t, sess, "doc_list", scope(map[string]any{"kind": "specs"}))
	if docs, _ := out["docs"].([]any); len(docs) != 1 || !strings.HasPrefix(text, "Worklode has 1 spec.") {
		t.Errorf("doc_list = %q %v", text, out)
	}
}

func TestMCPRuleAddLink(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	mustCall(t, sess, "project_use", map[string]any{"project": "worklode"})

	_, out := mustCall(t, sess, "rule_add", map[string]any{"heading": "Logins expire", "body": "A login lasts a day."})
	first, _ := out["rule"].(map[string]any)["ref"].(string)
	text, out := mustCall(t, sess, "file", map[string]any{"what": "requirement", "title": "Expiry is configurable"})
	second, _ := out["ref"].(string)
	if out["entity"] != "rule" || !strings.HasPrefix(text, "Added draft requirement "+second) {
		t.Fatalf("file(requirement) = %q %v", text, out)
	}

	// "requirement <n>" in words reaches the same rule.
	n := strings.TrimPrefix(second, "WL-REQ-")
	text, out = mustCall(t, sess, "rule_link", map[string]any{"ref": "requirement " + n, "rel": "refines", "target": first})
	if text != second+" now refines "+first+"." || out["ref"] != second {
		t.Errorf("rule_link = %q %v", text, out)
	}
	rr := doReq(t, f.h, http.MethodGet, "/api/v1/rules/"+second, f.token, nil)
	var rule model.Rule
	decodeInto(t, rr, &rule)
	if !slices.ContainsFunc(rule.Edges, func(e model.RuleEdge) bool { return e.Type == "refines" }) {
		t.Errorf("rule %s edges = %+v, want a refines edge", second, rule.Edges)
	}
	if _, _, isErr := callTool(t, sess, "rule_link", map[string]any{"ref": second, "rel": "amends", "target": first}); !isErr {
		t.Error("rule_link accepted rel amends")
	}

	text, out = mustCall(t, sess, "rule_list", nil)
	if rules, _ := out["rules"].([]any); len(rules) != 2 || !strings.HasPrefix(text, "Worklode has 2 rules.") {
		t.Errorf("rule_list = %q %v", text, out)
	}
	text, _ = mustCall(t, sess, "show", map[string]any{"ref": second})
	if !strings.HasPrefix(text, "Here is requirement "+second) {
		t.Errorf("show rule = %q", text)
	}
}

func TestMCPPermissionRefused(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	admin := f.connect(t, f.token)
	_, out := mustCall(t, admin, "doc_add", map[string]any{"project": "WL", "kind": "spec", "title": "Guarded"})
	ref := out["doc"].(map[string]any)["ref"].(string)
	_, out = mustCall(t, admin, "task_add", map[string]any{"project": "WL", "kind": "chore", "title": "Bound"})
	taskID := out["task"].(map[string]any)["id"].(string)

	// A task-scoped token may read and author documents, but accepting one
	// is not on its route grant: the normal guard refuses it.
	bound := mintTaskToken(t, f.h, f.token, taskID, nil)
	sess := f.connect(t, bound.Token)
	mustCall(t, sess, "doc_list", map[string]any{"project": "WL"})
	text, _, isErr := callTool(t, sess, "doc_transition", map[string]any{"project": "WL", "id": ref, "action": "accept"})
	if !isErr || !strings.HasPrefix(text, "Worklode refused this:") {
		t.Errorf("accept with a task token = %q (error %v)", text, isErr)
	}

	rr := doReq(t, f.admin, http.MethodGet, "/metrics", "", nil)
	body, _ := io.ReadAll(rr.Body)
	for _, want := range []string{
		`worklode_mcp_tool_calls_total{outcome="refused",tool="doc_transition"} 1`,
		`worklode_mcp_tool_calls_total{outcome="ok",tool="doc_list"} 1`,
		`worklode_mcp_tool_calls_total{outcome="ok",tool="doc_add"} 1`,
		`worklode_mcp_tool_calls_total{outcome="project_required",tool="search"} 0`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}

// An explicit project argument must name a project exactly; a near miss is
// not_found with the closest candidates, never a guess.
func TestMCPProjectArgumentIsExact(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	mustCall(t, sess, "task_list", map[string]any{"project": "DATA PLATFORM"})
	for _, arg := range []string{"data platfrm", "work load", "the data platform project"} {
		text, _, isErr := callTool(t, sess, "task_list", map[string]any{"project": arg})
		if !isErr || !strings.HasPrefix(text, "No Worklode project is called") {
			t.Errorf("task_list(project %q) = %q (error %v); want not_found", arg, text, isErr)
		}
	}
	text, _, _ := callTool(t, sess, "task_list", map[string]any{"project": "data platfrm"})
	if !strings.Contains(text, "Data Platform (DP)") || !strings.Contains(text, "ask the user") {
		t.Errorf("near miss lists no candidate: %q", text)
	}
}

// A full ref names its own project, so a tool given one needs no project.
func TestMCPFullRefNeedsNoProject(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	setup := f.connect(t, f.token)
	_, out := mustCall(t, setup, "task_add", map[string]any{"project": "DP", "kind": "chore", "title": "Tidy"})
	task := out["task"].(map[string]any)["id"].(string)
	_, out = mustCall(t, setup, "doc_add", map[string]any{"project": "DP", "kind": "spec", "title": "Ingest"})
	doc := out["doc"].(map[string]any)["ref"].(string)
	_, out = mustCall(t, setup, "rule_add", map[string]any{"project": "DP", "heading": "One", "body": "First."})
	r1 := out["rule"].(map[string]any)["ref"].(string)
	_, out = mustCall(t, setup, "rule_add", map[string]any{"project": "DP", "heading": "Two", "body": "Second."})
	r2 := out["rule"].(map[string]any)["ref"].(string)

	sess := f.connect(t, f.token) // no project_use
	mustCall(t, sess, "task_update", map[string]any{"id": strings.ToLower(task), "title": "Tidy up"})
	mustCall(t, sess, "task_set_state", map[string]any{"id": task, "state": "abandoned"})
	mustCall(t, sess, "doc_edit", map[string]any{"id": doc, "title": "Ingest flow"})
	mustCall(t, sess, "show", map[string]any{"ref": doc})
	mustCall(t, sess, "rule_link", map[string]any{"ref": r2, "rel": "refines", "target": r1})
	mustCall(t, sess, "rule_edit", map[string]any{"ref": r1, "body": "First, again."})
	// A spoken ref still needs the project.
	if text, _, isErr := callTool(t, sess, "show", map[string]any{"ref": "spec one"}); !isErr || !strings.Contains(text, "project_required") {
		t.Errorf("show(spec one) without a project = %q", text)
	}
}

// A rule added but not arranged says which rule exists.
func TestMCPRuleAddArrangeFailureNamesTheRule(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	sess := f.connect(t, f.token)
	text, _, isErr := callTool(t, sess, "rule_add", map[string]any{
		"project": "WL", "heading": "Orphan", "body": "Text.", "doc": "WL-SPEC-99",
	})
	if !isErr || !strings.HasPrefix(text, `Added WL-REQ-1, "Orphan", but could not arrange it in WL-SPEC-99`) {
		t.Errorf("rule_add into a missing spec = %q", text)
	}
}

// The bearer scheme is matched without regard to case (RFC 7235).
func TestMCPBearerSchemeAnyCase(t *testing.T) {
	t.Parallel()
	f := newMCPFixture(t)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("Authorization", "bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize with a lower-case scheme = %d; want 200", resp.StatusCode)
	}
}
