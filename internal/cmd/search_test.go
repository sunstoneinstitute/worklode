package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// searchStub is a stub backbone for `lode search`: it records the query it
// was asked and answers with a canned response. docs is what GET /docs
// returns, which is how the CLI turns a hit's document row id into the
// reference a reader cites.
type searchStub struct {
	got  url.Values
	resp model.SearchResponse
	docs []model.Doc
	// status, when non-zero, replaces the response with an error of that code.
	status int
	// docCalls counts the doc-reference lookups, so a run that needs none can
	// assert it made none.
	docCalls int
}

// setupSearch stands up the stub plus a repo whose config scopes commands to
// project "proj", chdir'd into.
func setupSearch(t *testing.T, s *searchStub) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}
	cfg := "current_project = \"proj\"\nproject_key = \"WL\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".worklode", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	t.Chdir(repo)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		s.got = r.URL.Query()
		if s.status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte(`{"error":"unknown mode \"quantum\", want hybrid|dense|lexical"}`))
			return
		}
		writeTestJSON(t, w, s.resp)
	})
	mux.HandleFunc("GET /api/v1/docs", func(w http.ResponseWriter, r *http.Request) {
		s.docCalls++
		writeTestJSON(t, w, model.DocListResponse{Docs: s.docs})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
}

// hasLine reports whether out holds a line whose cells are exactly the ones
// given. Column padding varies with the widest row, so a line is compared by
// its content with runs of whitespace collapsed.
func hasLine(out string, cells ...string) bool {
	want := strings.Join(cells, " ")
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Join(strings.Fields(line), " ") == want {
			return true
		}
	}
	return false
}

// hitsOfEveryKind is one result set covering all three subject kinds: a doc
// section, a task, and a skill.
func hitsOfEveryKind() model.SearchResponse {
	return model.SearchResponse{
		Provider: "openai-compatible",
		Mode:     "hybrid",
		Hits: []model.SearchHit{
			{Kind: "doc", DocID: 7, Anchor: "sec-15.2", Title: "The ordered log",
				Excerpt: "the log is append-only", Score: 0.032, DenseRank: 1, LexicalRank: 1},
			{Kind: "task", TaskID: "WL-634", Title: "lode search",
				Excerpt: "the CLI verb", Score: 0.016, DenseRank: 2},
			{Kind: "skill", SkillID: 3, Title: "acme:tdd",
				Excerpt: "red green refactor", Score: 0.008, LexicalRank: 2},
		},
	}
}

// TestSearchRendersTheAddressLine covers the 040 §9 rendering: a document hit
// is addressed by its reference and frozen section anchor, a task by its id,
// a skill by its qualified name — and the query reaches the server as one
// string with the project scope attached.
func TestSearchRendersTheAddressLine(t *testing.T) {
	stub := &searchStub{
		resp: hitsOfEveryKind(),
		docs: []model.Doc{{ID: 7, Project: "proj", ProjectKey: "WL", Kind: "spec", Number: 25,
			Slug: "025-design-documents", Title: "The ordered log"}},
	}
	setupSearch(t, stub)

	out, err := runLode(t, "search", "the", "ordered", "log")
	if err != nil {
		t.Fatalf("search: %v\noutput: %s", err, out)
	}
	if got := stub.got.Get("q"); got != "the ordered log" {
		t.Errorf("q = %q; want the arguments joined into one query", got)
	}
	if got := stub.got.Get("project"); got != "proj" {
		t.Errorf("project = %q; want the scoped project", got)
	}
	// The columns are padded to align, so each line is matched by its cells.
	for _, want := range [][]string{
		{"WL-SPEC-25 §15.2", "0.032", "The ordered log"},
		{"WL-634", "0.016", "lode search"},
		{"acme:tdd", "0.008"},
	} {
		if !hasLine(out, want...) {
			t.Errorf("output missing a line reading %v\noutput:\n%s", want, out)
		}
	}
	// A skill's title is its qualified name; printing it twice on one line is
	// noise, not information.
	if hasLine(out, "acme:tdd", "0.008", "acme:tdd") {
		t.Errorf("skill line repeats the name:\n%s", out)
	}
}

// TestSearchFiltersReachTheServer: every flag is passed through, --kind is
// repeatable, and nothing the caller did not set is sent.
func TestSearchFiltersReachTheServer(t *testing.T) {
	stub := &searchStub{resp: model.SearchResponse{Provider: "openai-compatible", Mode: "lexical"}}
	setupSearch(t, stub)

	out, err := runLode(t, "search", "child_of",
		"--kind", "doc", "--kind", "task", "--mode", "lexical", "--limit", "5")
	if err != nil {
		t.Fatalf("search: %v\noutput: %s", err, out)
	}
	if got := stub.got["kind"]; len(got) != 2 || got[0] != "doc" || got[1] != "task" {
		t.Errorf("kind = %v; want both repeats", got)
	}
	if got := stub.got.Get("mode"); got != "lexical" {
		t.Errorf("mode = %q", got)
	}
	if got := stub.got.Get("limit"); got != "5" {
		t.Errorf("limit = %q", got)
	}

	// Unset flags leave the server's own defaults alone.
	stub2 := &searchStub{resp: model.SearchResponse{Provider: "openai-compatible", Mode: "hybrid"}}
	setupSearch(t, stub2)
	if _, err := runLode(t, "search", "child_of"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, ok := stub2.got["mode"]; ok {
		t.Errorf("mode sent without --mode: %v", stub2.got)
	}
	if _, ok := stub2.got["limit"]; ok {
		t.Errorf("limit sent without --limit: %v", stub2.got)
	}
}

// TestSearchJSON: --json emits the response body verbatim, per-arm ranks
// included, and costs no document lookup.
func TestSearchJSON(t *testing.T) {
	stub := &searchStub{resp: hitsOfEveryKind()}
	setupSearch(t, stub)

	out, err := runLode(t, "search", "the ordered log", "--json")
	if err != nil {
		t.Fatalf("search --json: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		`"provider":"openai-compatible"`, `"dense_rank":1`, `"lexical_rank":1`,
		`"anchor":"sec-15.2"`, `"excerpt":"the log is append-only"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--json output missing %s\noutput:\n%s", want, out)
		}
	}
	if stub.docCalls != 0 {
		t.Errorf("--json made %d document lookup(s); the raw hits need none", stub.docCalls)
	}
}

// TestSearchDegradedProviderIsANotice: an instance with no embedding provider
// answers lexically. That is a one-line notice on stderr and a zero exit,
// with the hits it did find on stdout (040 §11).
func TestSearchDegradedProviderIsANotice(t *testing.T) {
	stub := &searchStub{resp: model.SearchResponse{
		Provider: "none", Mode: "lexical",
		Hits: []model.SearchHit{{Kind: "task", TaskID: "WL-634", Title: "lode search",
			Score: 0.016, LexicalRank: 1}},
	}}
	setupSearch(t, stub)

	stdout, stderr, err := runLodeOutErr(t, "search", "child_of")
	if err != nil {
		t.Fatalf("search on a degraded instance failed: %v", err)
	}
	if !strings.Contains(stderr, "no embedding provider") || !strings.Contains(stderr, "lexical") {
		t.Errorf("stderr = %q; want the one-line degraded notice", stderr)
	}
	if !hasLine(stdout, "WL-634", "0.016", "lode search") {
		t.Errorf("stdout = %q; want the lexical hits it did find", stdout)
	}
	if strings.Contains(stdout, "no embedding provider") {
		t.Errorf("the notice belongs on stderr, not in the results:\n%s", stdout)
	}
}

// TestSearchServerErrorExitsNonZero: a rejected query is a failure, unlike a
// degraded provider, and the server's wording survives to the caller.
func TestSearchServerErrorExitsNonZero(t *testing.T) {
	setupSearch(t, &searchStub{status: http.StatusUnprocessableEntity})

	out, err := runLode(t, "search", "child_of", "--mode", "quantum")
	if err == nil {
		t.Fatalf("bad --mode exited 0\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "hybrid|dense|lexical") {
		t.Errorf("err = %v; want the server's own wording", err)
	}
}
