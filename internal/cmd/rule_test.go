package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRuleEditRequiresFile covers the one required flag: no --file, no
// network call, straight refusal.
func TestRuleEditRequiresFile(t *testing.T) {
	out, err := runLode(t, "rule", "edit", "WL-RULE-1")
	if err == nil {
		t.Fatalf("lode rule edit WL-RULE-1 (no --file) succeeded\noutput: %s", out)
	}
	want := `no file: pass a path, or "-" to read the body from stdin`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestRuleEditOnAcceptedSaysWhereItLanded: the write went to the arranging
// document's candidate revision, so the rule read back still carries the
// old text. The render alone reads as a no-op; the command has to name the
// document and the command that lands it.
func TestRuleEditOnAcceptedSaysWhereItLanded(t *testing.T) {
	accepted := model.Rule{
		Ref: "WL-RULE-1", Heading: "Sub", Body: "\nOld text.\n\n",
		Status: "accepted", Version: 1,
		ArrangedIn: []model.RuleArrangement{{DocRef: "WL-SPEC-12", Anchor: "sec-1.1", Depth: 3, RuleVersion: 1}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accepted)
	})
	mux.HandleFunc("PUT /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("New text.\n"), 0o600); err != nil {
		t.Fatalf("write fixture body: %v", err)
	}

	out, err := runLode(t, "rule", "edit", "WL-RULE-1", "--file", file)
	if err != nil {
		t.Fatalf("lode rule edit: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "candidate revision of WL-SPEC-12") ||
		!strings.Contains(out, "lode doc revise WL-SPEC-12 --accept") {
		t.Fatalf("output = %q; want it to name the document and how the change lands", out)
	}

	out, err = runLode(t, "rule", "edit", "WL-RULE-1", "--file", file, "--json")
	if err != nil {
		t.Fatalf("lode rule edit --json: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "candidate revision") {
		t.Fatalf("--json output carries the notice: %q", out)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("--json output is not valid JSON: %q", out)
	}
}

// TestRuleEditDraftSaysNothingExtra: on a draft document the write lands at
// once, so the notice above must not appear.
func TestRuleEditDraftSaysNothingExtra(t *testing.T) {
	mux := http.NewServeMux()
	reply := model.Rule{
		Ref: "WL-RULE-1", Heading: "Sub", Body: "\nNew text.\n\n", Status: "draft", Version: 1,
		ArrangedIn: []model.RuleArrangement{{DocRef: "WL-SPEC-12", Anchor: "sec-1.1", Depth: 3, RuleVersion: 1}},
	}
	mux.HandleFunc("GET /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("PUT /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(reply)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("New text.\n"), 0o600); err != nil {
		t.Fatalf("write fixture body: %v", err)
	}
	out, err := runLode(t, "rule", "edit", "WL-RULE-1", "--file", file)
	if err != nil {
		t.Fatalf("lode rule edit: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "candidate revision") {
		t.Fatalf("draft edit printed the revision notice: %q", out)
	}
}

// TestRuleEditReadsStdin: --file - reads the body from stdin, the way
// every sibling body-taking command does.
func TestRuleEditReadsStdin(t *testing.T) {
	var putBody model.EditRuleInput
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Rule{Ref: "WL-RULE-1", Heading: "Sub", Status: "draft", Version: 1})
	})
	mux.HandleFunc("PUT /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Fatalf("decode PUT body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Rule{Ref: "WL-RULE-1", Heading: putBody.Heading, Body: putBody.Body, Status: "draft", Version: 1})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	resetFlags(t, rootCmd)
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader("Piped body.\n"))
	defer rootCmd.SetIn(nil)
	rootCmd.SetArgs([]string{"rule", "edit", "WL-RULE-1", "--file", "-"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("lode rule edit --file -: %v\noutput: %s", err, buf.String())
	}
	if putBody.Body != "Piped body.\n" {
		t.Fatalf("PUT body = %q; want the piped text", putBody.Body)
	}
}

// TestRuleEditResolvesHeadingLocally covers the decided fix: when
// --heading is omitted, the current heading is fetched and used, and that
// resolution never leaks into a later invocation. Two calls in the same
// process, against two different current headings, must each pick up their
// own server response rather than the first call's.
func TestRuleEditResolvesHeadingLocally(t *testing.T) {
	heading := "Heading A"
	var putBody model.EditRuleInput
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Rule{Ref: "WL-RULE-1", Heading: heading, Status: "draft", Version: 1})
	})
	mux.HandleFunc("PUT /api/v1/rules/WL-RULE-1", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Fatalf("decode PUT body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Rule{Ref: "WL-RULE-1", Heading: putBody.Heading, Body: putBody.Body, Status: "draft", Version: 2})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("New body text.\n"), 0o600); err != nil {
		t.Fatalf("write fixture body: %v", err)
	}

	out, err := runLode(t, "rule", "edit", "WL-RULE-1", "--file", file)
	if err != nil {
		t.Fatalf("lode rule edit WL-RULE-1 --file %s: %v\noutput: %s", file, err, out)
	}
	if putBody.Heading != "Heading A" {
		t.Fatalf("PUT heading = %q; want %q", putBody.Heading, "Heading A")
	}
	if !strings.Contains(out, "Heading A") {
		t.Fatalf("output = %q; want it to render the resolved heading", out)
	}

	heading = "Heading B"
	out, err = runLode(t, "rule", "edit", "WL-RULE-1", "--file", file)
	if err != nil {
		t.Fatalf("lode rule edit WL-RULE-1 --file %s (2nd run): %v\noutput: %s", file, err, out)
	}
	if putBody.Heading != "Heading B" {
		t.Fatalf("2nd run PUT heading = %q; want %q (not the 1st run's leaked value)", putBody.Heading, "Heading B")
	}
}

// TestRuleVersionsCommand covers `lode rule versions <ref>` rendering
// the version-history table.
func TestRuleVersionsCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rules/WL-RULE-1/versions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"version":2,"heading":"Sub","created_at":"2026-09-22T10:00:00Z"},{"version":1,"heading":"Sub","created_at":"2026-09-21T10:00:00Z"}]`)
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	out, err := runLode(t, "rule", "versions", "WL-RULE-1")
	if err != nil {
		t.Fatalf("lode rule versions WL-RULE-1: %v\noutput: %s", err, out)
	}
	if !strings.HasPrefix(out, "VERSION") || !strings.Contains(out, "2") || !strings.Contains(out, "1") {
		t.Fatalf("output = %q; want a version table with both versions", out)
	}
}

// TestRuleSupersedeCommand covers `lode rule supersede --map <file>
// --project <p>` end to end: it reads the map, posts it to the project's
// supersede route, and renders the result.
func TestRuleSupersedeCommand(t *testing.T) {
	var posted model.SupersedeInput
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/cow/rules/supersede" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Fatalf("decode POST body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.SupersedeResult{
			Entries:   []model.SupersedeResolved{{Old: "WL-RULE-2", New: []string{"WL-RULE-4", "WL-RULE-5"}}, {Old: "WL-RULE-3"}},
			Withdrawn: 2, Edges: 2,
		})
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "map.txt")
	mapBody := "# a comment\nWL-RULE-2 -> WL-RULE-4 WL-RULE-5\nWL-RULE-3 ->\n"
	if err := os.WriteFile(file, []byte(mapBody), 0o600); err != nil {
		t.Fatalf("write fixture map: %v", err)
	}

	out, err := runLode(t, "rule", "supersede", "--map", file, "--project", "cow")
	if err != nil {
		t.Fatalf("lode rule supersede: %v\noutput: %s", err, out)
	}
	if len(posted.Entries) != 2 || posted.Entries[0].Old != "WL-RULE-2" || len(posted.Entries[0].New) != 2 {
		t.Fatalf("posted entries = %+v", posted.Entries)
	}
	if posted.DryRun {
		t.Errorf("posted DryRun = true, want false")
	}
	if !strings.Contains(out, "WL-RULE-2 -> WL-RULE-4, WL-RULE-5") || !strings.Contains(out, "WL-RULE-3 ->") ||
		!strings.Contains(out, "withdrawn 2, edges 2") {
		t.Fatalf("output = %q; want the rendered entries and counts", out)
	}
	if strings.Contains(out, "dry run:") {
		t.Fatalf("output = %q; want no dry-run prefix on an applied map", out)
	}

	out, err = runLode(t, "rule", "supersede", "--project", "cow")
	if err == nil {
		t.Fatalf("supersede with no --map succeeded\noutput: %s", out)
	}
	want := `no map: pass --map <file>, or "-" to read it from stdin`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestParseSupersedeMap covers the map parser: a comment line, a blank line,
// a many-successor entry, a withdraw-only entry, and a malformed line naming
// its line number.
func TestParseSupersedeMap(t *testing.T) {
	in := "# a comment\n\nWL-RULE-1 -> WL-RULE-2 WL-RULE-3\nWL-RULE-4 ->\n  # indented comment\n"
	entries, err := parseSupersedeMap(in)
	if err != nil {
		t.Fatalf("parseSupersedeMap: %v", err)
	}
	want := []model.SupersedeEntry{
		{Old: "WL-RULE-1", New: []string{"WL-RULE-2", "WL-RULE-3"}},
		{Old: "WL-RULE-4", New: nil},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries = %+v, want %+v", entries, want)
	}

	_, err = parseSupersedeMap("WL-RULE-1 -> WL-RULE-2\nWL-RULE-3 WL-RULE-4\n")
	if err == nil {
		t.Fatal("malformed line (no \"->\") did not error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want it to name line 2", err.Error())
	}

	_, err = parseSupersedeMap(" -> WL-RULE-2\n")
	if err == nil {
		t.Fatal("empty left side did not error")
	}
}

// TestRuleAddAndAcceptCommands: add posts the heading, body, tags, kind and
// resolved project; accept posts to the rule's accept route.
func TestRuleAddAndAcceptCommands(t *testing.T) {
	var posted model.AddRuleInput
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/rules" {
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Fatalf("decode POST body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Rule{Ref: "WL-RULE-7", Heading: "H", Kind: "invariant", Status: "draft", Version: 1})
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("Body.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runLode(t, "rule", "add", "--heading", "H", "--file", file, "--tag", "a", "--tag", "b", "--kind", "invariant", "--project", "cow")
	if err != nil {
		t.Fatalf("lode rule add: %v\noutput: %s", err, out)
	}
	want := model.AddRuleInput{Project: "cow", Heading: "H", Body: "Body.\n", Tags: []string{"a", "b"}, Kind: "invariant"}
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("posted = %+v, want %+v", posted, want)
	}
	if !strings.Contains(out, "WL-RULE-7") {
		t.Errorf("output = %q, want the rule ref", out)
	}
	if _, err := runLode(t, "rule", "accept", "WL-RULE-7"); err != nil {
		t.Fatal(err)
	}
	if last := paths[len(paths)-1]; last != "POST /api/v1/rules/WL-RULE-7/accept" {
		t.Errorf("accept called %s", last)
	}
	if _, err := runLode(t, "rule", "add", "--file", file, "--project", "cow"); err == nil || err.Error() != "no heading: pass --heading <text>" {
		t.Errorf("add without heading: err = %v", err)
	}
}

// TestRuleLint covers `lode rule lint` end to end: a positional reference is
// named with its rule in both human and --json output, and the command exits
// non-zero while such a finding exists; a clean project exits zero
// (WL-SPEC-77 §4c).
func TestRuleLint(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	specFile := writeDocFile(t, "---\nstatus: draft\n---\n# S\n\n## 1. A {#sec-1}\n\nAs said above.\n")
	if _, err := runLode(t, "doc", "add", "--project", "proj", "--kind", "spec", "--slug", "s", "--file", specFile); err != nil {
		t.Fatalf("doc add: %v", err)
	}
	out, err := runLode(t, "rule", "lint", "--project", "proj")
	if err == nil {
		t.Fatalf("rule lint: err = nil, want non-zero exit\noutput: %s", out)
	}
	if !strings.Contains(out, "positional-reference") || !strings.Contains(out, "REQ-1") || !strings.Contains(out, "above") {
		t.Errorf("rule lint output = %q, want the positional reference named with its rule", out)
	}
	out, _ = runLode(t, "rule", "lint", "--project", "proj", "--json")
	var l model.RuleLint
	if err := json.Unmarshal([]byte(out), &l); err != nil || l.Rules != 1 || len(l.Findings) != 1 {
		t.Errorf("rule lint --json = %q (%v)", out, err)
	}
	if _, _, err := c.CreateProject(context.Background(), model.CreateProjectInput{ID: "clean", Name: "Clean", Key: "CLEAN"}); err != nil {
		t.Fatalf("create project clean: %v", err)
	}
	if out, err := runLode(t, "rule", "lint", "--project", "clean"); err != nil {
		t.Errorf("rule lint (clean project): %v\noutput: %s", err, out)
	}
}

// TestRuleArrangeAndUnarrangeCommands: arrange posts the rule and position
// to the spec's rules route; unarrange deletes the rule from it.
func TestRuleArrangeAndUnarrangeCommands(t *testing.T) {
	var posted model.ArrangeRuleInput
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Fatalf("decode POST body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(model.Rule{Ref: "WL-REQ-3", Heading: "H", Status: "accepted", Version: 1})
	}))
	defer srv.Close()
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", "test-token")

	if out, err := runLode(t, "rule", "arrange", "7", "WL-REQ-3", "--under", "sec-2", "--anchor", "sec-2.4"); err != nil {
		t.Fatalf("lode rule arrange: %v\noutput: %s", err, out)
	}
	if want := (model.ArrangeRuleInput{Rule: "WL-REQ-3", Under: "sec-2", Anchor: "sec-2.4"}); posted != want {
		t.Errorf("posted = %+v, want %+v", posted, want)
	}
	if _, err := runLode(t, "rule", "unarrange", "7", "WL-REQ-3"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /api/v1/docs/7/rules", "DELETE /api/v1/docs/7/rules/WL-REQ-3"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("called %v, want %v", paths, want)
	}
	if _, err := runLode(t, "rule", "arrange", "7", "WL-REQ-3", "--after", "sec-1", "--under", "sec-2"); err == nil {
		t.Error("--after with --under did not error")
	}
}
