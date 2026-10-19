package mcp_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/corpusindex"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

type apiRT struct{ h http.Handler }

func (t apiRT) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

// client is a cli.Client over the fixture's API mux, for seeding.
func (f *fixture) client(token string) *cli.Client {
	return cli.NewClientWithTransport(cli.Config{ServerURL: "http://lode.test", Token: token}, apiRT{f.api})
}

func text(res *sdk.CallToolResult, i int) string {
	if len(res.Content) <= i {
		return ""
	}
	return res.Content[i].(*sdk.TextContent).Text
}

const specBody = "---\nstatus: draft\n---\n\n# Documents in the backbone\n\n## 1. Scope {#sec-1}\n\nScope body.\n\n## 2. Model {#sec-2}\n\nModel body.\n"

// seedAmendedSpec creates WL-SPEC-77 (rules WL-REQ-1, WL-REQ-2) and an
// accepted WL-SPEC-46 whose rule WL-REQ-3 amends WL-REQ-2.
func seedAmendedSpec(t *testing.T, c *cli.Client) {
	t.Helper()
	ctx := t.Context()
	for _, d := range []model.CreateDocInput{
		{Project: "worklode", Kind: "spec", Number: 77, Slug: "077-documents", Body: specBody},
		{Project: "worklode", Kind: "spec", Number: 46, Slug: "046-tighter",
			Body: "---\nstatus: draft\n---\n\n# Tighter\n\n## 1. Tighter model {#sec-1}\n\nThe model now says ten.\n"},
	} {
		doc, _, err := c.CreateDoc(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.AcceptDoc(ctx, doc.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.LinkRules(ctx, "WL-REQ-3", model.RuleEdgeInput{Type: "amends", To: "WL-REQ-2"}); err != nil {
		t.Fatal(err)
	}
}

func TestShow(t *testing.T) {
	f := newFixture(t)
	seedAmendedSpec(t, f.client(f.alice))
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))

	// A spoken ref shows the spec, amendments folded in by default.
	res := call(t, cs, "show", map[string]any{"project": h, "ref": "spec seventy-seven"})
	if res.IsError || !strings.HasPrefix(text(res, 0), "WL-SPEC-77 is the accepted spec") {
		t.Fatalf("show spoken = %q", text(res, 0))
	}
	if body := text(res, 1); !strings.Contains(body, "Scope body.") || !strings.Contains(body, "The model now says ten.") {
		t.Fatalf("inline body = %q", body)
	}

	// inline=false leaves the amendment out.
	res = call(t, cs, "show", map[string]any{"project": h, "ref": "WL-SPEC-77", "inline": false})
	if body := text(res, 1); !strings.Contains(body, "Model body.") || strings.Contains(body, "says ten") {
		t.Fatalf("not inline body = %q", body)
	}

	// section narrows, with the fold intact.
	res = call(t, cs, "show", map[string]any{"project": h, "ref": "WL-SPEC-77", "section": "2"})
	if body := text(res, 1); strings.Contains(body, "Scope body.") || !strings.Contains(body, "The model now says ten.") {
		t.Fatalf("section body = %q", body)
	}

	// A rule, by spoken ref, with its amendment.
	res = call(t, cs, "show", map[string]any{"project": h, "ref": "requirement two"})
	if res.IsError || !strings.HasPrefix(text(res, 0), "WL-REQ-2 is the accepted") || !strings.Contains(text(res, 1), "says ten") {
		t.Fatalf("show rule = %q / %q", text(res, 0), text(res, 1))
	}

	// A task and a project.
	task, _, err := f.client(f.alice).CreateTask(t.Context(), model.CreateTaskInput{Project: "worklode", Title: "Fix the thing", Kind: "bug", Priority: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	res = call(t, cs, "show", map[string]any{"ref": strings.ToLower(task.ID)})
	if !res.IsError {
		t.Fatalf("lowercase task id without a handle = %q, want project_required", text(res, 0))
	}
	res = call(t, cs, "show", map[string]any{"ref": task.ID}) // full ref: no handle needed
	if res.IsError || !strings.Contains(text(res, 0), "Fix the thing") {
		t.Fatalf("show task = %q", text(res, 0))
	}
	res = call(t, cs, "show", map[string]any{"project": h, "ref": "Rollout"})
	if res.IsError || text(res, 0) != "Rollout (RO) is a Worklode project." {
		t.Fatalf("show project = %q", text(res, 0))
	}

	// Unknown refs are not_found.
	for _, ref := range []string{"spec nine hundred", "WL-9999", "no-such-thing"} {
		if res := call(t, cs, "show", map[string]any{"project": h, "ref": ref}); !res.IsError {
			t.Fatalf("show %q = %q, want not_found", ref, text(res, 0))
		}
	}
	if n := f.count(t, "show", "not_found"); n != 3 {
		t.Fatalf("show not_found = %v, want 3", n)
	}
}

func TestSearchPages(t *testing.T) {
	f := newFixture(t)
	c := f.client(f.alice)
	for i := range 12 {
		task, _, err := c.CreateTask(t.Context(), model.CreateTaskInput{Project: "worklode", Title: fmt.Sprintf("Quokka %d", i), Kind: "feature", Priority: "medium"})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.st.ReplaceSubjectChunks(t.Context(), store.ChunkSubject{Kind: store.SubjectTask, TaskID: task.ID, Project: "worklode", ContentHash: "seed"},
			[]corpusindex.Chunk{{Text: "The quokka is a small marsupial."}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	cs := f.connect(t, f.alice, oldProtocol, nil)
	h := handleOf(t, call(t, cs, "project_use", map[string]any{"project": "WL", "confirmed": true}))

	res := call(t, cs, "search", map[string]any{"project": h, "query": "quokka"})
	if res.IsError || text(res, 0) != `Found 12 results for "quokka".` {
		t.Fatalf("search = %q", text(res, 0))
	}
	if n := strings.Count(text(res, 1), "Quokka"); n != 10 {
		t.Fatalf("first page has %d hits, want 10:\n%s", n, text(res, 1))
	}
	m := regexp.MustCompile(`cursor "([^"]+)"`).FindStringSubmatch(text(res, 1))
	if m == nil {
		t.Fatalf("no cursor in %q", text(res, 1))
	}
	res = call(t, cs, "search", map[string]any{"project": h, "query": "quokka", "cursor": m[1]})
	if body := text(res, 1); strings.Count(body, "Quokka") != 2 || strings.Contains(body, "cursor") {
		t.Fatalf("second page = %q", body)
	}
	if res := call(t, cs, "search", map[string]any{"project": h, "query": "quokka", "cursor": "10.forged"}); !res.IsError {
		t.Fatal("forged cursor accepted")
	}
	if res := call(t, cs, "search", map[string]any{"query": "quokka"}); !res.IsError {
		t.Fatal("search without a project ran")
	}
	if n := f.count(t, "search", "invalid"); n != 1 {
		t.Fatalf("search invalid = %v, want 1", n)
	}
}
