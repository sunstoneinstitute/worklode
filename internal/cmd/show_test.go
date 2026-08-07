package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		arg  string
		kind targetKind
		typ  string
	}{
		{"12", targetTask, ""},
		{"WL-12", targetTask, ""},
		{"WL-SPEC-23", targetDoc, ""},
		{"WL-ADR-7", targetDoc, ""},
		{"WL-SPEC-14#sec-2.1", targetDoc, ""},
		{"WL-SPEC-14#sec-a_b", targetDoc, ""},
		{"WL-PLAN-4-1", targetUnshowable, "PLAN"},
		{"WL-MILE-2", targetUnshowable, "MILE"},
		{"WL-DEL-3", targetUnshowable, "DEL"},
		{"XX-FOO-3", targetUnknownType, "FOO"},
		{"WL-SPEC-0", targetDoc, ""},
		{"wl-12", targetUnclassified, ""},
		{"garbage", targetUnclassified, ""},
		{"NO-SPEC", targetUnclassified, ""},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got := classify(tc.arg)
			if got.Kind != tc.kind {
				t.Fatalf("classify(%q).Kind = %v; want %v", tc.arg, got.Kind, tc.kind)
			}
			if got.Type != tc.typ {
				t.Fatalf("classify(%q).Type = %q; want %q", tc.arg, got.Type, tc.typ)
			}
		})
	}
}

// setupDocCorpus creates a fake $HOME containing a repo directory with a
// .worklode/config.toml (current_project and, when projectKey is non-empty,
// project_key) and docs/specs populated from files, chdirs into the repo, and
// returns its path. Mirrors setupRepoConfig (currentproject_test.go), plus
// the corpus FindCorpus/ResolveRef need.
func setupDocCorpus(t *testing.T, projectKey string, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config dir: %v", err)
	}
	content := "current_project = \"proj\"\n"
	if projectKey != "" {
		content += "project_key = \"" + projectKey + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(repo, ".worklode", "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	specs := filepath.Join(repo, "docs", "specs")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatalf("mkdir docs/specs: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(specs, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Chdir(repo)
	return repo
}

const fixtureSpec = `---
status: accepted
implements: NO-SPEC
---
# Spec 14 — Fixture

Intro text.

## 1. First {#sec-1}

Body of section 1.

### 1.1 Nested {#sec-1.1}

Body of nested section.

## 2. Second {#sec-2}

Body of section 2.
`

func TestShowTaskDispatch(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Shown via dispatcher")
	setupRepoConfig(t, "proj")

	// Full id.
	out, err := runLode(t, "show", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode show %s: %v\noutput: %s", task.ID, err, out)
	}
	var got cli.Task
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got.ID != task.ID {
		t.Fatalf("lode show %s = %+v; want id %s", task.ID, got, task.ID)
	}

	// Bare number, scoped by current_project.
	number := task.ID[strings.LastIndex(task.ID, "-")+1:]
	out, err = runLode(t, "show", number, "--json")
	if err != nil {
		t.Fatalf("lode show %s: %v\noutput: %s", number, err, out)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got.ID != task.ID {
		t.Fatalf("lode show %s = %+v; want id %s", number, got, task.ID)
	}
}

// TestShowSpecFlagEquivalence covers the rework's central equivalence claim:
// `--spec <n>` must produce exactly what the typed positional id produces.
func TestShowSpecFlagEquivalence(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	outFlag, err := runLode(t, "show", "--spec", "14")
	if err != nil {
		t.Fatalf("lode show --spec 14: %v\noutput: %s", err, outFlag)
	}
	if outFlag != fixtureSpec {
		t.Fatalf("show --spec 14 output = %q; want the fixture verbatim (%q)", outFlag, fixtureSpec)
	}

	outPositional, err := runLode(t, "show", "WL-SPEC-14")
	if err != nil {
		t.Fatalf("lode show WL-SPEC-14: %v\noutput: %s", err, outPositional)
	}
	if outFlag != outPositional {
		t.Fatalf("show --spec 14 = %q; want it to match positional WL-SPEC-14 = %q", outFlag, outPositional)
	}

	outKind, err := runLode(t, "show", "--kind", "spec", "14")
	if err != nil {
		t.Fatalf("lode show --kind spec 14: %v\noutput: %s", err, outKind)
	}
	if outKind != outFlag {
		t.Fatalf("show --kind spec 14 = %q; want it to match --spec 14 = %q", outKind, outFlag)
	}
}

func TestShowDispatchesSpecToDocShow(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-SPEC-14")
	if err != nil {
		t.Fatalf("lode show WL-SPEC-14: %v\noutput: %s", err, out)
	}
	if out != fixtureSpec {
		t.Fatalf("show output = %q; want the fixture verbatim", out)
	}
}

func TestDocShowSectionFragmentSugar(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-SPEC-14#sec-1")
	if err != nil {
		t.Fatalf("lode doc show #sec-1: %v\noutput: %s", err, out)
	}
	want := "## 1. First {#sec-1}\n\nBody of section 1.\n\n### 1.1 Nested {#sec-1.1}\n\nBody of nested section.\n\n"
	if out != want {
		t.Fatalf("doc show #sec-1 = %q; want %q (heading, body, and its whole subtree)", out, want)
	}
}

func TestDocShowSectionFlag(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-SPEC-14", "--section", "sec-2")
	if err != nil {
		t.Fatalf("lode doc show --section sec-2: %v\noutput: %s", err, out)
	}
	want := "## 2. Second {#sec-2}\n\nBody of section 2.\n"
	if out != want {
		t.Fatalf("doc show --section sec-2 = %q; want %q", out, want)
	}

	// A leading # is accepted too.
	out, err = runLode(t, "show", "WL-SPEC-14", "--section", "#sec-2")
	if err != nil {
		t.Fatalf("lode doc show --section #sec-2: %v\noutput: %s", err, out)
	}
	if out != want {
		t.Fatalf("doc show --section #sec-2 = %q; want %q", out, want)
	}
}

func TestDocShowFragmentAndFlagDisagree(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-SPEC-14#sec-1", "--section", "sec-2")
	if err == nil {
		t.Fatalf("fragment #sec-1 with --section sec-2 succeeded; want an error\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("err = %v; want it to say the two disagree", err)
	}
}

func TestDocShowUnknownSection(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-SPEC-14", "--section", "sec-9")
	if err == nil {
		t.Fatalf("--section sec-9 (no such section) succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "no section sec-9 in") {
		t.Fatalf("err = %v; want it to say no section sec-9", err)
	}
}

func TestDocShowJSON(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-SPEC-14", "--section", "sec-2", "--json")
	if err != nil {
		t.Fatalf("lode doc show --json: %v\noutput: %s", err, out)
	}
	var got struct {
		Path    string `json:"path"`
		Section string `json:"section"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got.Section != "sec-2" {
		t.Fatalf("section = %q; want sec-2", got.Section)
	}
	if !strings.HasSuffix(got.Path, "014-fixture.md") {
		t.Fatalf("path = %q; want it to name the fixture file", got.Path)
	}
	want := "## 2. Second {#sec-2}\n\nBody of section 2.\n"
	if got.Content != want {
		t.Fatalf("content = %q; want %q", got.Content, want)
	}
}

func TestDocShowForeignKeyUnresolvedExitsZero(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "OT-SPEC-14")
	if err != nil {
		t.Fatalf("lode doc show OT-SPEC-14: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "unresolved: project OT not known here") {
		t.Fatalf("output = %q; want the tier-3 unresolved message", out)
	}
}

// anchorlessBoundarySpec has, right after WL-SPEC-14's #sec-1 subtree, a
// heading with no number and no anchor — the boundary section that
// terminates #sec-1's subtree must never need one (a regression fixture for
// the anchorless-boundary bug).
const anchorlessBoundarySpec = `---
status: accepted
implements: NO-SPEC
---
# Spec 15 — Anchorless boundary fixture

Intro text.

## 1. First {#sec-1}

Body of section 1.

### 1.1 Nested {#sec-1.1}

Body of nested section.

## Unanchored trailer

Trailer body, must not be included.
`

func TestDocShowSectionStopsAtAnchorlessBoundary(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"015-fixture.md": anchorlessBoundarySpec})

	out, err := runLode(t, "show", "WL-SPEC-15", "--section", "sec-1")
	if err != nil {
		t.Fatalf("lode doc show --section sec-1: %v\noutput: %s", err, out)
	}
	want := "## 1. First {#sec-1}\n\nBody of section 1.\n\n### 1.1 Nested {#sec-1.1}\n\nBody of nested section.\n\n"
	if out != want {
		t.Fatalf("doc show --section sec-1 = %q; want %q (stopping at the anchorless boundary heading, not over-printing to EOF)", out, want)
	}
	if strings.Contains(out, "Unanchored trailer") || strings.Contains(out, "Trailer body") {
		t.Fatalf("doc show --section sec-1 leaked the anchorless boundary section's text:\n%s", out)
	}
}

// anchorlessNestedSpec has a legal anchorless H5 ("##### Deep unanchored
// aside", docs/authoring-design-docs.md §on H5/H6 content sitting inside
// their nearest anchored ancestor) nested INSIDE WL-SPEC-16's #sec-1
// subtree — extracting #sec-1 must still return that whole subtree rather
// than erroring because the H5 has no anchor of its own.
const anchorlessNestedSpec = `---
status: accepted
implements: NO-SPEC
---
# Spec 16 — Anchorless nested fixture

Intro text.

## 1. First {#sec-1}

Body of section 1.

##### Deep unanchored aside

Aside body text.

## 2. Second {#sec-2}

Body of section 2.
`

func TestDocShowSectionIncludesAnchorlessDescendant(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"016-fixture.md": anchorlessNestedSpec})

	out, err := runLode(t, "show", "WL-SPEC-16", "--section", "sec-1")
	if err != nil {
		t.Fatalf("lode doc show --section sec-1: %v\noutput: %s", err, out)
	}
	want := "## 1. First {#sec-1}\n\nBody of section 1.\n\n##### Deep unanchored aside\n\nAside body text.\n\n"
	if out != want {
		t.Fatalf("doc show --section sec-1 = %q; want %q (the anchorless H5 aside included, stopping before #sec-2)", out, want)
	}
}

// anchorlessBeforeTargetSpec has a legal anchorless H5 BEFORE WL-SPEC-17's
// #sec-1 — locating #sec-1 must not depend on that earlier heading carrying
// an anchor either.
const anchorlessBeforeTargetSpec = `---
status: accepted
implements: NO-SPEC
---
# Spec 17 — Anchorless before fixture

Intro text.

##### Unanchored intro aside

Aside body before the target.

## 1. First {#sec-1}

Body of section 1.
`

func TestDocShowFindsTargetPastAnchorlessSection(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"017-fixture.md": anchorlessBeforeTargetSpec})

	out, err := runLode(t, "show", "WL-SPEC-17", "--section", "sec-1")
	if err != nil {
		t.Fatalf("lode doc show --section sec-1: %v\noutput: %s", err, out)
	}
	want := "## 1. First {#sec-1}\n\nBody of section 1.\n"
	if out != want {
		t.Fatalf("doc show --section sec-1 = %q; want %q (found past the earlier anchorless heading)", out, want)
	}
}

func TestDocShowForeignKeyUnresolvedJSON(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "OT-SPEC-14", "--json")
	if err != nil {
		t.Fatalf("lode doc show OT-SPEC-14 --json: %v\noutput: %s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(got) != 1 {
		t.Fatalf("json = %v; want exactly one key (unresolved)", got)
	}
	if got["unresolved"] != "unresolved: project OT not known here" {
		t.Fatalf("unresolved = %v; want the tier-3 message", got["unresolved"])
	}
}

func TestShowMilestoneErrors(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "WL-MILE-2")
	if err == nil {
		t.Fatalf("lode show WL-MILE-2 succeeded\noutput: %s", out)
	}
	want := `WL-MILE-2 is a milestone id; milestones are not showable yet (spec 029 §4 defines them; the entities land with spec 029)`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

func TestShowUnknownTypeErrors(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	out, err := runLode(t, "show", "XX-FOO-3")
	if err == nil {
		t.Fatalf("lode show XX-FOO-3 succeeded\noutput: %s", out)
	}
	want := `unknown entity type "FOO" in XX-FOO-3; known types: SPEC, ADR, PLAN, MILE, DEL (a task id has no type segment: WL-12)`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

func TestDocShowNoCorpus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "outside")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(dir)

	out, err := runLode(t, "show", "WL-SPEC-14")
	if err == nil {
		t.Fatalf("doc show outside any worklode repo succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "not inside a worklode repo") {
		t.Fatalf("err = %v; want the no-corpus message", err)
	}
}

// fixtureADR is a minimal kind: adr document, so a WL-ADR-* ref has a real
// target to exercise the show -> runDocShow wiring end to end.
const fixtureADR = `---
status: accepted
kind: adr
---
# ADR 18 — Fixture decision

Decision body.
`

func TestShowDispatchesADRToDocShow(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"018-fixture-adr.md": fixtureADR})

	out, err := runLode(t, "show", "WL-ADR-18")
	if err != nil {
		t.Fatalf("lode show WL-ADR-18: %v\noutput: %s", err, out)
	}
	if out != fixtureADR {
		t.Fatalf("show output = %q; want the ADR fixture verbatim", out)
	}
}

// TestShowNoSpecSentinelIsNamed covers WL-SPEC-0, both as a typed positional
// id and via --spec 0 (which builds the same WL-SPEC-0 shorthand, per
// runDocShowByOrdinal). The bare "NO-SPEC" string itself is no longer
// reachable through `lode show`: with `lode doc show` gone, nothing routes an
// untyped ref straight to runDocShow, and "NO-SPEC" does not classify as a
// task or a typed id (TestClassify's targetUnclassified case).
func TestShowNoSpecSentinelIsNamed(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"014-fixture.md": fixtureSpec})

	for _, args := range [][]string{
		{"show", "WL-SPEC-0"},
		{"show", "--spec", "0"},
	} {
		out, err := runLode(t, args...)
		if err == nil {
			t.Fatalf("lode %s succeeded\noutput: %s", strings.Join(args, " "), out)
		}
		if !strings.Contains(err.Error(), "WL-SPEC-0") || !strings.Contains(err.Error(), "no-governing-spec sentinel") {
			t.Fatalf("err = %v; want it to name WL-SPEC-0 and explain the sentinel", err)
		}
	}
}

// trailingSpaceFrontmatterSpec closes its frontmatter with "--- " (a
// trailing space) and later has an exact "---" thematic break in the body.
// frontmatterEnd must still recognise the real close and stop scanning
// there — a regression fixture for the bug where the trailing space made
// the scan run past every earlier heading looking for an exact "---" match,
// finding the later thematic break instead and reporting "no section" for a
// section that plainly exists.
const trailingSpaceFrontmatterSpec = "---\nstatus: accepted\nimplements: NO-SPEC\n--- \n" +
	"# Spec 18 — Frontmatter trailing space fixture\n\n" +
	"Intro text.\n\n" +
	"## 1. First {#sec-1}\n\n" +
	"Body of section 1.\n\n" +
	"---\n\n" +
	"Trailing thematic break, not a frontmatter delimiter.\n"

func TestDocShowSectionSurvivesTrailingSpaceFrontmatterClose(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"018-fixture.md": trailingSpaceFrontmatterSpec})

	out, err := runLode(t, "show", "WL-SPEC-18", "--section", "sec-1")
	if err != nil {
		t.Fatalf("lode doc show --section sec-1: %v\noutput: %s", err, out)
	}
	want := "## 1. First {#sec-1}\n\nBody of section 1.\n\n---\n\nTrailing thematic break, not a frontmatter delimiter.\n"
	if out != want {
		t.Fatalf("doc show --section sec-1 = %q; want %q", out, want)
	}
}

// --- lode show <kind flags> -------------------------------------------

// TestShowADRFlagEquivalence mirrors TestShowSpecFlagEquivalence for --adr.
func TestShowADRFlagEquivalence(t *testing.T) {
	setupDocCorpus(t, "WL", map[string]string{"018-fixture-adr.md": fixtureADR})

	outFlag, err := runLode(t, "show", "--adr", "18")
	if err != nil {
		t.Fatalf("lode show --adr 18: %v\noutput: %s", err, outFlag)
	}
	if outFlag != fixtureADR {
		t.Fatalf("show --adr 18 output = %q; want the ADR fixture verbatim (%q)", outFlag, fixtureADR)
	}

	outPositional, err := runLode(t, "show", "WL-ADR-18")
	if err != nil {
		t.Fatalf("lode show WL-ADR-18: %v\noutput: %s", err, outPositional)
	}
	if outFlag != outPositional {
		t.Fatalf("show --adr 18 = %q; want it to match positional WL-ADR-18 = %q", outFlag, outPositional)
	}
}

// newShowTaskStubServer serves the two routes `lode show --task`/a bare
// number positional need, without a real store: GET /api/v1/projects (for
// cli.ProjectKey's project-key lookup, GetProject's own implementation) and
// GET /api/v1/tasks/{id} (GetTask), both returning a fixed canned body.
func newShowTaskStubServer(t *testing.T, taskBody string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[{"id":"proj","name":"Project","key":"PROJ","repos":[],"focus":[]}]}`))
	})
	mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(taskBody))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
}

// TestShowTaskFlagEquivalence covers `--task 1` == positional `1`, against a
// stub server rather than a real store-backed one (internal/cmd tests need
// no Postgres).
func TestShowTaskFlagEquivalence(t *testing.T) {
	const taskBody = `{"id":"PROJ-1","project":"proj","title":"Fixture task","state":"ready","priority":"medium","kind":"feature"}`
	newShowTaskStubServer(t, taskBody)
	setupRepoConfig(t, "proj")

	outFlag, err := runLode(t, "show", "--task", "1", "--json")
	if err != nil {
		t.Fatalf("lode show --task 1: %v\noutput: %s", err, outFlag)
	}
	outPositional, err := runLode(t, "show", "1", "--json")
	if err != nil {
		t.Fatalf("lode show 1: %v\noutput: %s", err, outPositional)
	}
	if outFlag != outPositional {
		t.Fatalf("show --task 1 = %q; want it to match positional 1 = %q", outFlag, outPositional)
	}
	if !strings.Contains(outFlag, `"PROJ-1"`) {
		t.Fatalf("output = %q; want the fixture task id", outFlag)
	}
}

// TestShowProjectFlag covers `lode show --project <id>` against the same
// canned-response stub `lode project show` itself tests (project_test.go).
func TestShowProjectFlag(t *testing.T) {
	d := newDetailServer(t, projectDetailBody)
	setupRepoConfig(t, "worklode")

	out, err := runLode(t, "show", "--project", "worklode")
	if err != nil {
		t.Fatalf("lode show --project worklode: %v\noutput: %s", err, out)
	}
	want := "worklode (WL) — Worklode\n" +
		"focus: correctness, throughput\n" +
		"repos:\n" +
		"  sunstoneinstitute/worklode  done: merged\n" +
		"\n" +
		"cost, last 30 days: 11.29 USD\n" +
		"  2026-07-30  0.41   in 1.2k  cache-w 40.1k   cache-r 900.3k  out 3.1k\n" +
		"  2026-07-31  10.88  in 2.0k  cache-w 354.0k  cache-r 11.8M   out 57.6k\n"
	if out != want {
		t.Fatalf("show --project worklode output:\n%s\nwant:\n%s", out, want)
	}
	if d.id != "worklode" {
		t.Fatalf("server saw project id %q, want worklode", d.id)
	}

	// --kind project <id> is the same routine.
	outKind, err := runLode(t, "show", "--kind", "project", "worklode")
	if err != nil {
		t.Fatalf("lode show --kind project worklode: %v\noutput: %s", err, outKind)
	}
	if outKind != want {
		t.Fatalf("show --kind project worklode = %q; want it to match --project worklode = %q", outKind, want)
	}
}

func TestShowErrorsTwoKindFlags(t *testing.T) {
	out, err := runLode(t, "show", "--spec", "1", "--adr", "2")
	if err == nil {
		t.Fatalf("lode show --spec 1 --adr 2 succeeded\noutput: %s", out)
	}
	if err.Error() != "pass only one kind flag" {
		t.Fatalf("err = %q; want %q", err.Error(), "pass only one kind flag")
	}
}

func TestShowErrorsKindFlagWithPositional(t *testing.T) {
	out, err := runLode(t, "show", "--spec", "1", "WL-SPEC-2")
	if err == nil {
		t.Fatalf("lode show --spec 1 WL-SPEC-2 succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v; want it to say the flag and positional are mutually exclusive", err)
	}
}

func TestShowErrorsUnknownKind(t *testing.T) {
	out, err := runLode(t, "show", "--kind", "bogus", "1")
	if err == nil {
		t.Fatalf("lode show --kind bogus succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), `unknown kind "bogus"`) {
		t.Fatalf("err = %v; want it to name the unknown kind", err)
	}
	for _, k := range showKinds {
		if !strings.Contains(err.Error(), k) {
			t.Fatalf("err = %v; want it to list valid kind %q", err, k)
		}
	}
}

func TestShowErrorsKindWithoutPositional(t *testing.T) {
	out, err := runLode(t, "show", "--kind", "spec")
	if err == nil {
		t.Fatalf("lode show --kind spec succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "--kind spec requires exactly one positional argument") {
		t.Fatalf("err = %v; want it to say --kind spec needs a positional argument", err)
	}
}

func TestShowErrorsSpecFlagTakesIDNotOrdinal(t *testing.T) {
	out, err := runLode(t, "show", "--spec", "WL-SPEC-15")
	if err == nil {
		t.Fatalf("lode show --spec WL-SPEC-15 succeeded\noutput: %s", out)
	}
	want := "--spec takes a bare ordinal, not an id; pass either --spec 15 or the id WL-SPEC-15 positionally"
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestShowErrorsSpecFlagEmptyValue covers ordinalShapeError's fallback case:
// an empty value carries no recoverable ordinal, so the positional
// suggestion (which would otherwise read "...or the id  positionally", with
// a dangling double space) must not be offered at all.
func TestShowErrorsSpecFlagEmptyValue(t *testing.T) {
	out, err := runLode(t, "show", "--spec", "")
	if err == nil {
		t.Fatalf("lode show --spec '' succeeded\noutput: %s", out)
	}
	want := `--spec takes a bare ordinal (e.g. --spec 15); "" is not one`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestShowErrorsDeliverableFlagNonNumeric covers the same fallback for a
// non-numeric, non-typed-id value: suggesting "abc" positionally would just
// fail again, so the short form names the value instead.
func TestShowErrorsDeliverableFlagNonNumeric(t *testing.T) {
	out, err := runLode(t, "show", "--deliverable", "abc")
	if err == nil {
		t.Fatalf("lode show --deliverable abc succeeded\noutput: %s", out)
	}
	want := `--deliverable takes a bare ordinal (e.g. --deliverable 15); "abc" is not one`
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

func TestShowErrorsSectionWithTask(t *testing.T) {
	out, err := runLode(t, "show", "--task", "1", "--section", "sec-1")
	if err == nil {
		t.Fatalf("lode show --task 1 --section sec-1 succeeded\noutput: %s", out)
	}
	if err.Error() != "--section applies only to specs and ADRs" {
		t.Fatalf("err = %q; want %q", err.Error(), "--section applies only to specs and ADRs")
	}
}

func TestShowMilestoneFlagErrors(t *testing.T) {
	out, err := runLode(t, "show", "--milestone", "2")
	if err == nil {
		t.Fatalf("lode show --milestone 2 succeeded\noutput: %s", out)
	}
	want := "milestone 2 is not showable yet (spec 029 §4 defines them; the entities land with spec 029)"
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestShowPlanFlagOrdinalShape guards showOrdinalShape["plan"]'s
// `^\d+(-\d+)?$` regex — the branch's only kind-specific ordinal shape
// (019 §4.3a) — by exercising its second-ordinal form ("4-1") end to end.
func TestShowPlanFlagOrdinalShape(t *testing.T) {
	out, err := runLode(t, "show", "--plan", "4-1")
	if err == nil {
		t.Fatalf("lode show --plan 4-1 succeeded\noutput: %s", out)
	}
	want := "plan 4-1 is not showable yet (spec 029 §4 defines them; the entities land with spec 029)"
	if err.Error() != want {
		t.Fatalf("err = %q; want %q", err.Error(), want)
	}
}

// TestShowAdrFlagKeylessStillChecksKind covers a review fix: with no
// project_key configured, --spec/--adr fall back to ResolveRef's bare-number
// form (form 2), which never runs CheckKind on its own, unlike the
// <KEY>-SPEC-<n>/<KEY>-ADR-<n> shorthand form (3) the flag path uses when the
// key IS known — so runDocShow's expectedKind parameter must enforce the
// kind independently in the keyless case too. --adr pointing at a spec file
// must still error with the kind mismatch, and --spec must still render
// normally: a flag always means the local corpus, key or no key, so
// resolving by number with no key is legitimate for --spec/--adr — unlike a
// positional shorthand id (WL-SPEC-15), which would instead get 026 §4.2's
// tier-3 "unresolved" treatment for an unknown foreign key.
func TestShowAdrFlagKeylessStillChecksKind(t *testing.T) {
	setupDocCorpus(t, "", map[string]string{
		"014-fixture.md":     fixtureSpec,
		"018-fixture-adr.md": fixtureADR,
	})

	// --adr 14 names a document that is actually a spec: still a
	// KindMismatchError, not a silent spec render.
	out, err := runLode(t, "show", "--adr", "14")
	if err == nil {
		t.Fatalf("lode show --adr 14 (a spec) succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "ref names an ADR, document is a spec") {
		t.Fatalf("err = %v; want the kind-mismatch message", err)
	}

	// --spec 14 on the same keyless corpus still renders normally.
	out, err = runLode(t, "show", "--spec", "14")
	if err != nil {
		t.Fatalf("lode show --spec 14: %v\noutput: %s", err, out)
	}
	if out != fixtureSpec {
		t.Fatalf("show --spec 14 output = %q; want the fixture verbatim", out)
	}

	// Symmetric case: --spec 18 names an actual ADR.
	out, err = runLode(t, "show", "--spec", "18")
	if err == nil {
		t.Fatalf("lode show --spec 18 (an ADR) succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "ref names a spec, document is an ADR") {
		t.Fatalf("err = %v; want the kind-mismatch message", err)
	}
}

// TestShowHasNoDocCommand pins the removal of `lode doc`: its verbs now live
// under `lode show`'s kind flags.
func TestShowHasNoDocCommand(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "doc" {
			t.Fatalf("rootCmd still has a %q child command; lode doc must be removed", c.Name())
		}
	}
	out, err := runLode(t, "doc", "show", "26")
	if err == nil {
		t.Fatalf(`lode doc show 26 succeeded\noutput: %s`, out)
	}
}
