package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// todoSpec is a two-section spec: sec-1 is covered by a plan, sec-2 by
// nothing, so one run exercises both a plan-level item and a collapsed
// planning gap.
const todoSpec = `---
status: accepted
covers: NO-SPEC
---
# Spec 1 — Example

Intro.

## 1. First {#sec-1}

Body.

## 2. Second {#sec-2}

Body.
`

// todoPlanOpen covers sec-1 in full and names an execution task, so its item
// type depends entirely on what the closure lookup says about WL-1.
const todoPlanOpen = `---
status: accepted
task: WL-1
covers:
  - spec: docs/specs/001-example.md#sec-1
    coverage: full
---
# Plan 1-1 — Build the first section

Body.
`

// todoServer counts what one `lode doc todo` run costs: the corpus is read
// through GET /docs plus one GET /docs/{id} per document, and task closure
// through a single GET /tasks. The body fetches run concurrently, so the
// counters are atomic.
type todoServer struct{ listCalls, bodyCalls, taskCalls atomic.Int64 }

// setupTodoCorpus stands up a repo and the backbone that serves its corpus:
// specs and plans keyed by the corpus filename each document was imported
// from, since that name minus ".md" is the slug the backbone stores
// (docimport.go) and designdoc.CorpusPath turns back into a path. tasks is
// the GET /tasks body the closure lookup reads.
//
// Nothing is written to disk under the corpus directories: the command reads
// documents from the backbone, so it must work in a checkout that holds none.
func setupTodoCorpus(t *testing.T, specs, plans map[string]string, tasks string) *todoServer {
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

	var docs []model.Doc
	bodies := map[int64]string{}
	add := func(files map[string]string, planCorpus bool) {
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			id := int64(len(docs) + 1)
			slug := strings.TrimSuffix(name, ".md")
			kind := "plan"
			number := 0
			if !planCorpus {
				kind = "spec"
				parsed, err := designdoc.Parse([]byte(files[name]))
				if err != nil {
					t.Fatalf("parse fixture %s: %v", name, err)
				}
				if parsed.Frontmatter != nil && parsed.Frontmatter.Kind == "adr" {
					kind = "adr"
				}
				if m := docFixtureNumber.FindStringSubmatch(slug); m != nil {
					number, _ = strconv.Atoi(m[1])
				}
			}
			docs = append(docs, model.Doc{
				ID: id, Project: "proj", Kind: kind, Number: number,
				Slug: slug, Title: slug, Status: "accepted", Version: 1,
			})
			bodies[id] = files[name]
		}
	}
	add(specs, false)
	add(plans, true)

	srv := &todoServer{}
	mux := http.NewServeMux()
	// The org's projects, which 026 §4.2's tier 2 resolves a shorthand key
	// through — the same path `lode show` takes.
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, model.ProjectListResponse{Projects: []model.Project{
			{ID: "proj", Name: "Proj", Key: "WL"},
		}})
	})
	mux.HandleFunc("GET /api/v1/docs", func(w http.ResponseWriter, r *http.Request) {
		srv.listCalls.Add(1)
		// The real list blanks bodies (withoutDocBodies); a walk that leaned
		// on them would pass here otherwise.
		project := r.URL.Query().Get("project")
		listed := make([]model.Doc, 0, len(docs))
		for _, d := range docs {
			if project == "" || d.Project == project {
				listed = append(listed, d)
			}
		}
		writeTestJSON(t, w, model.DocListResponse{Docs: listed})
	})
	mux.HandleFunc("GET /api/v1/docs/{id}", func(w http.ResponseWriter, r *http.Request) {
		srv.bodyCalls.Add(1)
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		for _, d := range docs {
			if d.ID != id {
				continue
			}
			d.Body = bodies[id]
			writeTestJSON(t, w, model.DocDetail{Doc: d})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		srv.taskCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tasks))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return srv
}

// noTasks is the empty task list: every plan's task reads as unknown, which
// is the state a fixture that says nothing about tasks wants.
const noTasks = `{"tasks":[]}`

// TestDocTodoTable covers the table: both item shapes and the document
// header. The task list is empty, so the covering plan's task reads as
// unknown rather than dropping its item.
func TestDocTodoTable(t *testing.T) {
	setupTodoCorpus(t,
		map[string]string{"001-example.md": todoSpec},
		map[string]string{"001-1-first.md": todoPlanOpen}, noTasks)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-1")
	if err != nil {
		t.Fatalf("doc todo: %v\noutput: %s", err, out)
	}
	for _, want := range []string{
		"docs/specs/001-example.md",
		"unplanned",
		"sec-2",
		"1 section has no covering plan",
		"unexecuted",
		"sec-1",
		// The plan column drops the corpus directory the reader already
		// knows; the --json items keep the full path (TestDocTodoJSON).
		"001-1-first.md",
		"task WL-1: state unknown",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out)
		}
	}
	if strings.Contains(out, "docs/plans/") {
		t.Errorf("plan column still carries the corpus directory:\n%s", out)
	}
	// The gap detail must not leak a frontmatter key into prose.
	if strings.Contains(out, "fullCoverageWith") {
		t.Errorf("output leaks the fullCoverageWith frontmatter key:\n%s", out)
	}
}

// TestDocTodoClosureCostsOneRequest covers the online path: one list call
// answers every plan's task, a closed task drops its item, and an item whose
// task the response does not carry reports the state as unknown.
func TestDocTodoClosureCostsOneRequest(t *testing.T) {
	const plans = `---
status: accepted
task: WL-1
covers:
  - spec: docs/specs/001-example.md#sec-1
    coverage: full
---
# Plan 1-1 — First

Body.
`
	const secondPlan = `---
status: accepted
task: WL-9
covers:
  - spec: docs/specs/001-example.md#sec-2
    coverage: full
---
# Plan 1-2 — Second

Body.
`
	// WL-1's state does not look done and the server calls it closed anyway:
	// closure is the server's per-repo predicate, never a state string
	// (026 §2.5), so an implementation reading `state` fails here.
	srv := setupTodoCorpus(t,
		map[string]string{"001-example.md": todoSpec},
		map[string]string{"001-1-first.md": plans, "001-2-second.md": secondPlan},
		`{"tasks":[`+
			`{"id":"WL-1","project":"proj","title":"one","state":"ready","closed":true},`+
			`{"id":"WL-2","project":"proj","title":"two","state":"merged","closed":false}`+
			`]}`)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-1")
	if err != nil {
		t.Fatalf("doc todo: %v\noutput: %s", err, out)
	}
	// One list, one body per document, one task list: the corpus costs a
	// request per document because only GET /docs/{id} carries frontmatter,
	// but closure is asked once for the whole project, never per plan.
	list, body, task := srv.listCalls.Load(), srv.bodyCalls.Load(), srv.taskCalls.Load()
	if list != 1 || body != 3 || task != 1 {
		t.Errorf("requests = %d list, %d body, %d task; want 1, 3, 1",
			list, body, task)
	}
	// WL-1 is closed: its plan owes nothing and must not appear.
	if strings.Contains(out, "001-1-first.md") {
		t.Errorf("closed task's plan still reported:\n%s", out)
	}
	// WL-9 is absent from the response: known == false.
	if !strings.Contains(out, "task WL-9: state unknown") {
		t.Errorf("absent task not reported as unknown:\n%s", out)
	}
}

// TestDocTodoUnreachableServerFails pins that an unreachable backbone is an
// error, not a narrower answer: the corpus lives in the backbone, so there is
// no half of this question a client can answer without it.
func TestDocTodoUnreachableServerFails(t *testing.T) {
	setupTodoCorpus(t,
		map[string]string{"001-example.md": todoSpec},
		map[string]string{"001-1-first.md": todoPlanOpen}, noTasks)
	t.Setenv("LODE_SERVER", "http://127.0.0.1:1")
	t.Setenv("LODE_TOKEN", "test-token")

	out, err := runLode(t, "doc", "todo", "WL-SPEC-1")
	if err == nil {
		t.Fatalf("doc todo against an unreachable server succeeded\noutput: %s", out)
	}
}

// TestDocTodoJSON pins the one-document shape: items and diagnostics as
// sibling keys, so a consumer reads both from one parse. It decodes into
// locally-declared mirrors rather than the production types, so a renamed
// json tag fails here instead of round-tripping through its own change.
func TestDocTodoJSON(t *testing.T) {
	setupTodoCorpus(t,
		map[string]string{"001-example.md": todoSpec},
		map[string]string{"001-1-first.md": todoPlanOpen}, noTasks)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-1", "--json")
	if err != nil {
		t.Fatalf("doc todo --json: %v\noutput: %s", err, out)
	}
	var got struct {
		Items []struct {
			Type    string   `json:"type"`
			Doc     string   `json:"doc"`
			Anchor  string   `json:"anchor"`
			Anchors []string `json:"anchors"`
			Heading string   `json:"heading"`
			Plan    string   `json:"plan"`
			Task    string   `json:"task"`
			Detail  string   `json:"detail"`
		} `json:"items"`
		Diagnostics struct {
			Unfollowed []string `json:"unfollowed"`
			Cycles     []string `json:"cycles"`
			Notes      []string `json:"notes"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode --json output: %v\noutput: %s", err, out)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d; want 2\noutput: %s", len(got.Items), out)
	}
	if got.Items[0].Type != "unplanned" || len(got.Items[0].Anchors) != 1 || got.Items[0].Anchors[0] != "sec-2" {
		t.Errorf("first item = %+v; want the collapsed unplanned gap over sec-2", got.Items[0])
	}
	if got.Items[0].Doc != "docs/specs/001-example.md" || got.Items[0].Heading == "" || got.Items[0].Detail == "" {
		t.Errorf("first item = %+v; want doc, heading and detail populated", got.Items[0])
	}
	if got.Items[1].Type != "unexecuted" || got.Items[1].Anchor != "sec-1" ||
		got.Items[1].Plan != "docs/plans/001-1-first.md" || got.Items[1].Task != "WL-1" {
		t.Errorf("second item = %+v; want the unexecuted plan item", got.Items[1])
	}
	// The keys themselves are the contract: decoding into a struct proves
	// only that the tags agree with themselves.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, key := range []string{"items", "diagnostics"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("--json document has no %q key: %s", key, out)
		}
	}
	var diagKeys map[string]json.RawMessage
	if err := json.Unmarshal(doc["diagnostics"], &diagKeys); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	for _, key := range []string{"unfollowed", "cycles", "notes"} {
		if _, ok := diagKeys[key]; !ok {
			t.Errorf("diagnostics has no %q key: %s", key, out)
		}
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(doc["items"], &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	// The collapsed gap carries anchors and no anchor/plan/task; the plan
	// item is the mirror image. Both sets of names are pinned.
	for _, want := range []string{"type", "doc", "anchors", "heading", "detail"} {
		if _, ok := items[0][want]; !ok {
			t.Errorf("collapsed item has no %q key: %s", want, out)
		}
	}
	for _, want := range []string{"type", "doc", "anchor", "heading", "plan", "task", "detail"} {
		if _, ok := items[1][want]; !ok {
			t.Errorf("plan item has no %q key: %s", want, out)
		}
	}
}

// TestDocTodoTruncatesAnchorList pins that a collapsed gap naming many
// sections is truncated with a count rather than wrapping the terminal.
func TestDocTodoTruncatesAnchorList(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\nstatus: accepted\ncovers: NO-SPEC\n---\n# Spec 2 — Wide\n\nIntro.\n\n")
	for i := 1; i <= 30; i++ {
		n := strconv.Itoa(i)
		b.WriteString("## " + n + ". Section {#sec-" + n + "}\n\nBody.\n\n")
	}
	setupTodoCorpus(t, map[string]string{"002-wide.md": b.String()}, nil, noTasks)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-2")
	if err != nil {
		t.Fatalf("doc todo: %v\noutput: %s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 120 {
			t.Fatalf("line is %d chars, wider than a terminal:\n%s", len(line), line)
		}
	}
	if !strings.Contains(out, "…(30)") {
		t.Errorf("truncated anchor list does not name the total count:\n%s", out)
	}
	if !strings.Contains(out, "30 sections have no covering plan") {
		t.Errorf("plural gap detail missing:\n%s", out)
	}
}

// TestDocTodoPrintsShortAnchorListWhole covers the other side of that rule: a
// list that fits is printed entire, rather than eliding one entry behind an
// ellipsis that costs as much room as the entry would have.
func TestDocTodoPrintsShortAnchorListWhole(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\nstatus: accepted\ncovers: NO-SPEC\n---\n# Spec 3 — Narrow\n\nIntro.\n\n")
	for i := 1; i <= 4; i++ {
		n := strconv.Itoa(i)
		b.WriteString("## " + n + ". Section {#sec-" + n + "}\n\nBody.\n\n")
	}
	setupTodoCorpus(t, map[string]string{"003-narrow.md": b.String()}, nil, noTasks)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-3")
	if err != nil {
		t.Fatalf("doc todo: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "sec-1,sec-2,sec-3,sec-4") {
		t.Errorf("short anchor list was not printed whole:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("short anchor list was truncated anyway:\n%s", out)
	}
}

// TestDocTodoResolvesShorthandWithoutProjectKey is WL-348: `.worklode/config.toml`
// carries no project_key, so every <KEY>-<TYPE>-<n> ref — the checkout's own
// included — misses tier 1 and is the backbone's to resolve (026 §4.2 tier 2).
// It used to print "unresolved: project WL not known here" and exit 0.
func TestDocTodoResolvesShorthandWithoutProjectKey(t *testing.T) {
	setupTodoCorpus(t,
		map[string]string{"001-example.md": todoSpec},
		map[string]string{"001-1-first.md": todoPlanOpen}, noTasks)
	// setupTodoCorpus chdir'd into the repo it wrote; drop the key line.
	if err := os.WriteFile(filepath.Join(".worklode", "config.toml"),
		[]byte("current_project = \"proj\"\n"), 0o600); err != nil {
		t.Fatalf("rewrite repo config: %v", err)
	}

	out, err := runLode(t, "doc", "todo", "WL-SPEC-1")
	if err != nil {
		t.Fatalf("doc todo WL-SPEC-1: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "docs/specs/001-example.md") || !strings.Contains(out, "unplanned") {
		t.Errorf("output is not the spec's work list:\n%s", out)
	}
}

// TestDocTodoRefErrors covers the three ref outcomes that are not a document:
// a key no project carries is tier-3 unresolved, NO-SPEC is an error rather
// than an empty run, and a plan is not a starting point. All three exit
// nonzero — this command's exit status means "work remains" for a document it
// resolved, so a ref it could not resolve must not read as "no work" (WL-348).
func TestDocTodoRefErrors(t *testing.T) {
	setupTodoCorpus(t,
		map[string]string{"001-example.md": todoSpec},
		map[string]string{"001-1-first.md": todoPlanOpen, "009-1-other.md": todoPlanOpen}, noTasks)

	out, err := runLode(t, "doc", "todo", "OT-SPEC-1")
	if err == nil {
		t.Errorf("an unresolved ref exited 0\noutput: %s", out)
	} else if !strings.Contains(err.Error(), "unresolved: project OT not known here") {
		t.Errorf("err = %v; want the tier-3 unresolved message", err)
	}

	if out, err = runLode(t, "doc", "todo", "WL-SPEC-0"); err == nil {
		t.Errorf("NO-SPEC resolved to a walk\noutput: %s", out)
	} else if !strings.Contains(err.Error(), "no-governing-spec sentinel") {
		t.Errorf("err = %v; want the sentinel explained", err)
	}

	// A plan now resolves (029 §4 gave it a number), so it refuses at
	// designdoc.Todo's own "this walk starts from a spec or ADR" guard, which
	// says why rather than reporting the ref as naming nothing. The ref names
	// plan 009-1 rather than 001-1 because a ref that matches no path falls
	// through to the number form, where "001-1-first" would resolve to spec 1.
	if out, err = runLode(t, "doc", "todo", "docs/plans/009-1-other.md"); err == nil {
		t.Errorf("a plan was accepted as a starting point\noutput: %s", out)
	} else if !strings.Contains(err.Error(), "this walk starts from a spec or ADR") {
		t.Errorf("err = %v; want it to refuse the plan path", err)
	}
}

// TestDocTodoDepsLabelsDocuments pins that --deps output says which document
// each item belongs to, and that the walk order is printed unchanged.
func TestDocTodoDepsLabelsDocuments(t *testing.T) {
	const required = `---
status: accepted
covers: NO-SPEC
---
# Spec 3 — Required

Intro.

## 1. Only {#sec-1}

Body.
`
	requiring := strings.Replace(todoSpec, "covers: NO-SPEC\n",
		"covers: NO-SPEC\nrequires: docs/specs/003-required.md\n", 1)
	setupTodoCorpus(t,
		map[string]string{"001-example.md": requiring, "003-required.md": required},
		map[string]string{"001-1-first.md": todoPlanOpen}, noTasks)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-1", "--deps")
	if err != nil {
		t.Fatalf("doc todo --deps: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "docs/specs/001-example.md") || !strings.Contains(out, "docs/specs/003-required.md") {
		t.Fatalf("--deps output does not label both documents:\n%s", out)
	}
	if strings.Index(out, "docs/specs/001-example.md") > strings.Index(out, "docs/specs/003-required.md") {
		t.Errorf("--deps reordered the walk: the named document must lead\n%s", out)
	}

	// Without --deps the unfollowed edge is named in the footer instead.
	out, err = runLode(t, "doc", "todo", "WL-SPEC-1")
	if err != nil {
		t.Fatalf("doc todo: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "unfollowed") || !strings.Contains(out, "003-required.md") {
		t.Errorf("footer does not name the unfollowed requires edge:\n%s", out)
	}
}

// TestDocTodoEmptyListSaysSo pins that a finished spec prints a statement,
// not a blank screen (026 §2.5).
func TestDocTodoEmptyListSaysSo(t *testing.T) {
	const covered = `---
status: accepted
covers: NO-SPEC
---
# Spec 4 — Done

Intro.

## 1. Only {#sec-1}

Body.
`
	const donePlan = `---
status: accepted
task: WL-1
covers:
  - spec: docs/specs/004-done.md#sec-1
    coverage: full
---
# Plan 4-1 — Done

Body.
`
	// Again a closed task whose state string does not look done.
	setupTodoCorpus(t,
		map[string]string{"004-done.md": covered},
		map[string]string{"004-1-done.md": donePlan},
		`{"tasks":[{"id":"WL-1","project":"proj","title":"one","state":"ready","closed":true}]}`)

	out, err := runLode(t, "doc", "todo", "WL-SPEC-4")
	if err != nil {
		t.Fatalf("doc todo: %v\noutput: %s", err, out)
	}
	want := "nothing outstanding: every section of docs/specs/004-done.md is planned and executed\n"
	if out != want {
		t.Errorf("finished spec printed %q; want %q", out, want)
	}
}
