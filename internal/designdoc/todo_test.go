package designdoc_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// buildTodoCorpus writes specFiles and planFiles under a temp repo root and
// loads them with the real loader, so Todo is exercised against the
// CorpusDocs it will actually see. Both directories are relative, making
// CorpusDoc.Path repo-relative — the same shape buildIndex uses.
func buildTodoCorpus(t *testing.T, specFiles, planFiles map[string]string) []designdoc.CorpusDoc {
	t.Helper()
	t.Chdir(t.TempDir())
	const specDir, planDir = "docs/specs", "docs/plans"
	for dir, files := range map[string]map[string]string{specDir: specFiles, planDir: planFiles} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			writeDoc(t, dir, name, content)
		}
	}
	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	return docs
}

// renderItems reduces items to one comparable line each: everything but the
// Detail prose, which tests assert on separately when it matters.
func renderItems(items []designdoc.TodoItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		where := it.Anchor
		if len(it.Anchors) > 0 {
			where = strings.Join(it.Anchors, ",")
		}
		out[i] = fmt.Sprintf("%s %s#%s plan=%s task=%s", it.Type, it.Doc, where, it.Plan, it.Task)
	}
	return out
}

func checkItems(t *testing.T, got []designdoc.TodoItem, want []string) {
	t.Helper()
	lines := renderItems(got)
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("items =\n  %s\nwant\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
}

// twoSectionSpec is an accepted spec with two anchored sections, the fixture
// most section-level cases need.
const twoSectionSpec = `---
status: accepted
issued: 2026-01-01
---
# Spec 001 — Example

## 1. First {#sec-1}

Body.

## 2. Second {#sec-2}

Body.
`

const todoSpecRef = "docs/specs/001-example.md"

func closedSet(ids ...string) func(string) (bool, bool) {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id string) (bool, bool) { return set[id], true }
}

// allKnownOpen answers every task as open and known — the "server reachable,
// nothing closed" baseline.
func allKnownOpen(string) (bool, bool) { return false, true }

func TestTodoUnplannedSectionsCollapse(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{"001-example.md": twoSectionSpec}, nil)
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	// One item for the document, not one per section: writing a plan is a
	// single act and one plan covers many sections (026 §2.4).
	checkItems(t, items, []string{"unplanned " + todoSpecRef + "#sec-1,sec-2 plan= task="})
	if items[0].Anchor != "" {
		t.Errorf("Anchor = %q, want empty on a collapsed item", items[0].Anchor)
	}
	// Heading is the document's title on a collapsed item, and the section's
	// on a plan-level one (asserted in TestTodoUnexecuted).
	if items[0].Heading != "Spec 001 — Example" {
		t.Errorf("Heading = %q, want the document title", items[0].Heading)
	}
	if len(diag.Unfollowed) != 0 || len(diag.Cycles) != 0 {
		t.Errorf("diagnostics = %+v, want empty", diag)
	}
}

func TestTodoPartialSection(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n    coverage: partial\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n    coverage: none\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: closedSet("WL-1")})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	// sec-2 is bound only: no item at any plan status. The accepted plan's
	// task is closed, so it owes no execution item either.
	checkItems(t, items, []string{"partial " + todoSpecRef + "#sec-1 plan= task="})
}

// A bound-only section is not owed work (026 §2.4): the accepted plan read
// it and undertook nothing.
func TestTodoBoundOnlyEmitsNothing(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n    coverage: none\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n    coverage: none\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: closedSet("WL-1")})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, nil)
}

// A deferred section (026 §5.3, §2.1) emits no item, the same as a
// bound-only one: §2.5's five types are each discharged by an act this
// document's own plans can perform, and the next act on a deferred section
// belongs to its named owner, not to writing a plan here. This is the WL-290
// regression case: before defers was indexed by NewPlanIndex, this section
// read as `unplanned` and produced a (wrong) unplanned item; it must not now
// silently produce a mis-typed one either.
func TestTodoDeferredEmitsNothing(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ndefers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n" +
				"    to: docs/specs/006-knowledge-graph.md\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n" +
				"    to: docs/specs/006-knowledge-graph.md\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, nil)
}

// A *draft* plan's `defers` claim binds nothing yet, the same as a draft
// plan's `none` claim (026 §2.1's not-draft rule): the section is still
// unplanned.
func TestTodoDraftDefersIsStillUnplanned(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: draft\ndefers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n" +
				"    to: docs/specs/006-knowledge-graph.md\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n" +
				"    to: docs/specs/006-knowledge-graph.md\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unplanned " + todoSpecRef + "#sec-1,sec-2 plan= task="})
}

// A *draft* plan's `none` claim binds nothing yet: it discharges no coverage
// (026 §2.1's not-draft rule), so the section is still unplanned — and
// accepting the plan would discharge nothing about it, so there is no
// plan-draft item either.
func TestTodoDraftBoundOnlyIsStillUnplanned(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: draft\ncovers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n    coverage: none\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n    coverage: none\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unplanned " + todoSpecRef + "#sec-1,sec-2 plan= task="})
}

func TestTodoPlanDraftReplacesTheSectionGap(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: draft\ncovers:\n" +
				"  - " + todoSpecRef + "#sec-1\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	// The collapsed gap ranks ahead of the document's plan items: nothing
	// blocks writing a plan (026 §2.4).
	checkItems(t, items, []string{
		"unplanned " + todoSpecRef + "#sec-2 plan= task=",
		"plan-draft " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=",
	})
}

func TestTodoUnexecuted(t *testing.T) {
	plans := map[string]string{
		"a.md": "---\nstatus: accepted\ncovers: " + todoSpecRef + "#sec-1\n---\n# A\n\nBody.\n",
		"b.md": "---\nstatus: accepted\ntask: WL-2\ncovers: " + todoSpecRef + "#sec-2\n---\n# B\n\nBody.\n",
	}
	docs := buildTodoCorpus(t, map[string]string{"001-example.md": twoSectionSpec}, plans)
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=",
		"unexecuted " + todoSpecRef + "#sec-2 plan=docs/plans/b.md task=WL-2",
	})
	if items[0].Heading != "First" {
		t.Errorf("Heading = %q, want the section heading on a plan-level item", items[0].Heading)
	}

	// Closing WL-2 discharges b entirely; a still names no task.
	items, _, err = designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: closedSet("WL-2")})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task="})
}

// One accepted plan covering several sections owes one execution item, not
// one per section: executing it is a single act.
func TestTodoUnexecutedIsPerPlan(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - " + todoSpecRef + "#sec-1\n  - " + todoSpecRef + "#sec-2\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1"})
}

func TestTodoSupersededPlanEmitsNothing(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: superseded\ncovers:\n" +
				"  - " + todoSpecRef + "#sec-1\n  - " + todoSpecRef + "#sec-2\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, nil)
}

func TestTodoBlockedByUndischargedRequirement(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n" +
				"requires:\n  - b.md\n---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: accepted\ntask: WL-2\ncovers: " + todoSpecRef + "#sec-2\n---\n# B\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	// b ranks before a: a requires it, and topological order beats the
	// spec's own section order.
	checkItems(t, items, []string{
		"unexecuted " + todoSpecRef + "#sec-2 plan=docs/plans/b.md task=WL-2",
		"blocked " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
	if !strings.Contains(items[1].Detail, "docs/plans/b.md") {
		t.Errorf("blocked Detail = %q, want it to name the blocking plan", items[1].Detail)
	}

	// Closing b's task discharges it, so a is merely unexecuted.
	items, _, err = designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: closedSet("WL-2")})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1"})
}

// A draft spec leads with the acceptance decision and still reports its
// sections: the item ranks first, it does not replace the walk (026 §2.4).
func TestTodoDraftSpecLeadsWithAcceptanceItem(t *testing.T) {
	draft := strings.Replace(twoSectionSpec, "status: accepted", "status: draft", 1)
	docs := buildTodoCorpus(t, map[string]string{"001-example.md": draft}, nil)
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"plan-draft " + todoSpecRef + "# plan= task=",
		"unplanned " + todoSpecRef + "#sec-1,sec-2 plan= task=",
	})
}

// The acceptance item leads its document's collapsed planning gap, which in
// turn leads its plan items — the three document-level ranks in order.
func TestTodoDraftSpecAcceptanceItemIsFirst(t *testing.T) {
	draft := strings.Replace(twoSectionSpec, "status: accepted", "status: draft", 1)
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": draft},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"plan-draft " + todoSpecRef + "# plan= task=",
		"unplanned " + todoSpecRef + "#sec-2 plan= task=",
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
	if items[0].Anchor != "" || len(items[0].Anchors) != 0 {
		t.Errorf("leading item addresses %q/%v, want the document itself", items[0].Anchor, items[0].Anchors)
	}
}

// A replaced section is dropped only once the replacing document is
// accepted; a draft's claim is pending and drops nothing (026 §3.1).
func TestTodoCurrentSections(t *testing.T) {
	replacer := func(status string) string {
		return "---\nstatus: " + status + "\nreplaces:\n  \"#sec-1\":\n    - " + todoSpecRef + "#sec-2\n---\n" +
			"# Spec 002 — Replacer\n\n## 1. Instead {#sec-1}\n\nBody.\n"
	}
	for _, tc := range []struct {
		status string
		want   []string
	}{
		{"accepted", []string{"unplanned " + todoSpecRef + "#sec-1 plan= task="}},
		{"draft", []string{"unplanned " + todoSpecRef + "#sec-1,sec-2 plan= task="}},
	} {
		t.Run(tc.status, func(t *testing.T) {
			docs := buildTodoCorpus(t, map[string]string{
				"001-example.md":  twoSectionSpec,
				"002-replacer.md": replacer(tc.status),
			}, nil)
			items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
			if err != nil {
				t.Fatalf("Todo: %v", err)
			}
			checkItems(t, items, tc.want)
		})
	}
}

// A superseded document contributes no sections at all, and says so rather
// than reporting an unexplained empty list.
func TestTodoSupersededSpec(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md": strings.Replace(twoSectionSpec, "status: accepted", "status: superseded", 1),
	}, nil)
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, nil)
	if len(diag.Notes) == 0 {
		t.Error("Notes is empty, want the superseded document noted")
	}
}

const requiresSpecA = `---
status: accepted
issued: 2026-01-01
requires:
- 002-bee.md
- 003-missing.md
---
# Spec 001 — Example

## 1. First {#sec-1}

Body.
`

const requiresSpecB = `---
status: accepted
issued: 2026-01-01
requires:
- 001-example.md
---
# Spec 002 — Bee

## 1. Beefirst {#sec-1}

Body.
`

func TestTodoWithoutDepsListsUnfollowed(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md": requiresSpecA,
		"002-bee.md":     requiresSpecB,
	}, nil)
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unplanned " + todoSpecRef + "#sec-1 plan= task="})
	// Every outgoing edge, not just the first: the narrower answer must
	// never be mistaken for the whole one.
	want := []string{
		"001-example.md requires 002-bee.md",
		"001-example.md requires 003-missing.md",
	}
	if strings.Join(diag.Unfollowed, "|") != strings.Join(want, "|") {
		t.Errorf("Unfollowed = %v, want %v", diag.Unfollowed, want)
	}
}

// 025 and 026 require each other; the flag must survive exactly that pair.
func TestTodoDepsToleratesCycles(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md": requiresSpecA,
		"002-bee.md":     requiresSpecB,
	}, nil)
	items, diag, err := designdoc.Todo(docs, todoSpecRef,
		designdoc.TodoOptions{Deps: true, Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unplanned " + todoSpecRef + "#sec-1 plan= task=",
		"unplanned docs/specs/002-bee.md#sec-1 plan= task=",
	})
	if len(diag.Cycles) != 1 || !strings.Contains(diag.Cycles[0], "001-example.md") {
		t.Errorf("Cycles = %v, want the 001/002 cycle recorded", diag.Cycles)
	}
	// A requires target that is not a spec here is unfollowed even under
	// --deps, with the reason.
	if len(diag.Unfollowed) != 1 || !strings.Contains(diag.Unfollowed[0], "not a spec in this corpus") {
		t.Errorf("Unfollowed = %v, want the dangling target reported", diag.Unfollowed)
	}
}

// Repeated runs over an unchanged corpus must be byte-identical. This does
// not prove determinism on its own — that comes from the implementation
// ranging over no map — but it is what would break first if it started to.
func TestTodoOutputIsStableAcrossRuns(t *testing.T) {
	specs := map[string]string{
		"001-example.md": requiresSpecA,
		"002-bee.md":     requiresSpecB,
	}
	plans := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		plans[name+".md"] = "---\nstatus: accepted\ntask: WL-" + name + "\ncovers:\n" +
			"  - spec: " + todoSpecRef + "#sec-1\n    coverage: partial\n" +
			"---\n# Plan " + name + "\n\nBody.\n"
	}
	docs := buildTodoCorpus(t, specs, plans)
	opts := designdoc.TodoOptions{Deps: true, Closed: allKnownOpen}
	first, firstDiag, err := designdoc.Todo(docs, todoSpecRef, opts)
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, diag, err := designdoc.Todo(docs, todoSpecRef, opts)
		if err != nil {
			t.Fatalf("Todo: %v", err)
		}
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", first) {
			t.Fatalf("run %d differs:\n%+v\nwant\n%+v", i, got, first)
		}
		if fmt.Sprintf("%+v", diag) != fmt.Sprintf("%+v", firstDiag) {
			t.Fatalf("run %d diagnostics differ:\n%+v\nwant\n%+v", i, diag, firstDiag)
		}
	}
}

// Without a closure lookup the planning half still answers; the task level
// degrades to "unknown" and the footer says so.
func TestTodoOffline(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - " + todoSpecRef + "#sec-1\n  - " + todoSpecRef + "#sec-2\n---\n# A\n\nBody.\n",
		})
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1"})
	if len(diag.Notes) == 0 {
		t.Error("Notes is empty, want the offline degradation noted")
	}
	if !strings.Contains(items[0].Detail, "unknown") {
		t.Errorf("Detail = %q, want it to name the unknown task state", items[0].Detail)
	}
}

// A task the tracker does not know is not evidence of closure.
func TestTodoUnknownTaskIsUnexecuted(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{
		Closed: func(string) (bool, bool) { return false, false },
	})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unplanned " + todoSpecRef + "#sec-2 plan= task=",
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
	if !strings.Contains(items[1].Detail, "unknown") {
		t.Errorf("Detail = %q, want it to name the unknown task state", items[1].Detail)
	}
}

func TestTodoRefErrors(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{"001-example.md": twoSectionSpec}, nil)
	for _, ref := range []string{"docs/specs/404-missing.md", "", "docs/plans/nope.md"} {
		if _, _, err := designdoc.Todo(docs, ref, designdoc.TodoOptions{}); err == nil {
			t.Errorf("Todo(%q) succeeded, want an error", ref)
		}
	}
	// Task 4 dispatches on the sentinel, so the wrap must survive.
	_, _, err := designdoc.Todo(docs, "NO-SPEC", designdoc.TodoOptions{})
	if !errors.Is(err, designdoc.ErrNoSpec) {
		t.Errorf("Todo(\"NO-SPEC\") err = %v, want ErrNoSpec", err)
	}
}

// Any §4 reference form addresses the same document.
func TestTodoRefForms(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{"001-example.md": twoSectionSpec}, nil)
	for _, ref := range []string{
		"001-example.md",
		"docs/specs/001-example.md",
		"/docs/specs/001-example.md",
		"docs/specs/001-example.md#sec-1",
	} {
		items, _, err := designdoc.Todo(docs, ref, designdoc.TodoOptions{Closed: allKnownOpen})
		if err != nil {
			t.Fatalf("Todo(%q): %v", ref, err)
		}
		if len(items) != 1 || len(items[0].Anchors) != 2 {
			t.Errorf("Todo(%q) = %v, want one collapsed item over both sections", ref, renderItems(items))
		}
	}
}

// Offline never emits `blocked`: it is a statement about another plan's task
// state, which is exactly what is unavailable (026 §2.4). An item's type must
// not depend on the caller's connectivity.
func TestTodoOfflineNeverBlocked(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n" +
				"requires:\n  - b.md\n---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: accepted\ntask: WL-2\ncovers: " + todoSpecRef + "#sec-2\n---\n# B\n\nBody.\n",
		})
	online, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	if online[1].Type != "blocked" {
		t.Fatalf("online items = %v, want a blocked item", renderItems(online))
	}
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unexecuted " + todoSpecRef + "#sec-2 plan=docs/plans/b.md task=WL-2",
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
	if len(diag.Notes) == 0 {
		t.Error("Notes is empty, want the degradation named")
	}
}

// Two plans requiring each other block each other forever. The walk must not
// hang, and the loop belongs in the footer: silent mutual blocking is
// indistinguishable from real ordering.
func TestTodoPlanRequiresCycle(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n" +
				"requires:\n  - b.md\n---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: accepted\ntask: WL-2\ncovers: " + todoSpecRef + "#sec-2\n" +
				"requires:\n  - a.md\n---\n# B\n\nBody.\n",
		})
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	// Both are blocked, each naming the other. Their relative order comes
	// from ranks the cycle makes arbitrary — but the walk fixes the entry
	// point, so it is the same order every run.
	checkItems(t, items, []string{
		"blocked " + todoSpecRef + "#sec-2 plan=docs/plans/b.md task=WL-2",
		"blocked " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
	if len(diag.Cycles) != 1 || !strings.Contains(diag.Cycles[0], "a.md -> b.md -> a.md") {
		t.Errorf("Cycles = %v, want the a/b plan cycle recorded once", diag.Cycles)
	}
}

// A superseded requirement is spent, so it blocks nothing — the whole point
// of reading `superseded` as discharging (026 §2.1).
func TestTodoSupersededRequirementDoesNotBlock(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n" +
				"requires:\n  - b.md\n---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: superseded\ncovers: " + todoSpecRef + "#sec-2\n---\n# B\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
}

// A requirement this corpus does not hold cannot be judged, and an unjudgeable
// requirement is not a blocker.
func TestTodoRequirementOutsideCorpusDoesNotBlock(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - " + todoSpecRef + "#sec-1\n  - " + todoSpecRef + "#sec-2\n" +
				"requires:\n  - elsewhere.md\n---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
}

// An amended section still states the design, so it is kept — by omission
// today, which is exactly why it needs a test.
func TestTodoAmendedSectionIsKept(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md": twoSectionSpec,
		"002-amender.md": "---\nstatus: accepted\nissued: 2026-01-01\namends:\n  \"#sec-1\":\n    - " +
			todoSpecRef + "#sec-2\n---\n# Spec 002 — Amender\n\n## 1. More {#sec-1}\n\nBody.\n",
	}, nil)
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unplanned " + todoSpecRef + "#sec-1,sec-2 plan= task="})
}

// The supersession reading unions both directions, so the mirror edge alone
// still drops the section.
func TestTodoIsReplacedByDropsSection(t *testing.T) {
	spec := strings.Replace(twoSectionSpec, "issued: 2026-01-01",
		"issued: 2026-01-01\nisReplacedBy:\n  \"#sec-2\":\n    - 002-replacer.md#sec-1", 1)
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md":  spec,
		"002-replacer.md": "---\nstatus: accepted\nissued: 2026-01-01\n---\n# Spec 002 — Replacer\n\n## 1. Instead {#sec-1}\n\nBody.\n",
	}, nil)
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{"unplanned " + todoSpecRef + "#sec-1 plan= task="})
}

// A replaces with no fragment names the whole document, which drops all of it.
func TestTodoDocumentLevelReplaceDropsEverything(t *testing.T) {
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md": twoSectionSpec,
		"002-replacer.md": "---\nstatus: accepted\nissued: 2026-01-01\nreplaces:\n  \".\":\n    - " +
			todoSpecRef + "\n---\n# Spec 002 — Replacer\n\n## 1. Instead {#sec-1}\n\nBody.\n",
	}, nil)
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, nil)
	if len(diag.Notes) == 0 {
		t.Error("Notes is empty, want the dropped document explained")
	}
}

// A spec requiring a plan is not a document this walk descends into, with or
// without --deps.
func TestTodoDepsSkipsPlanRequires(t *testing.T) {
	spec := strings.Replace(twoSectionSpec, "issued: 2026-01-01",
		"issued: 2026-01-01\nrequires:\n- docs/plans/a.md", 1)
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": spec},
		map[string]string{"a.md": "---\nstatus: accepted\ncovers: NO-SPEC\n---\n# A\n\nBody.\n"})
	_, diag, err := designdoc.Todo(docs, todoSpecRef,
		designdoc.TodoOptions{Deps: true, Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	if len(diag.Unfollowed) != 1 || !strings.Contains(diag.Unfollowed[0], "not a spec in this corpus") {
		t.Errorf("Unfollowed = %v, want the plan target reported unfollowed", diag.Unfollowed)
	}
}

// A plan cycle is a corpus fact, so it is reported with no closure lookup
// too — the detection must sit above the online-only blocked decision.
func TestTodoPlanRequiresCycleOffline(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers: " + todoSpecRef + "#sec-1\n" +
				"requires:\n  - b.md\n---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: accepted\ntask: WL-2\ncovers: " + todoSpecRef + "#sec-2\n" +
				"requires:\n  - a.md\n---\n# B\n\nBody.\n",
		})
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	for _, it := range items {
		if it.Type == "blocked" {
			t.Errorf("offline items = %v, want no blocked item", renderItems(items))
			break
		}
	}
	if len(diag.Cycles) != 1 || !strings.Contains(diag.Cycles[0], "a.md -> b.md -> a.md") {
		t.Errorf("Cycles = %v, want the plan cycle recorded offline", diag.Cycles)
	}
}

// A draft plan in a requires cycle is still a cycle worth naming.
func TestTodoDraftPlanRequiresCycle(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: draft\ncovers: " + todoSpecRef + "#sec-1\n" +
				"requires:\n  - b.md\n---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: draft\ncovers: " + todoSpecRef + "#sec-2\n" +
				"requires:\n  - a.md\n---\n# B\n\nBody.\n",
		})
	items, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("items = %v, want one plan-draft item each", renderItems(items))
	}
	if len(diag.Cycles) != 1 || !strings.Contains(diag.Cycles[0], "a.md -> b.md -> a.md") {
		t.Errorf("Cycles = %v, want the draft plan cycle recorded", diag.Cycles)
	}
}

// An accepted plan covering a section only `partial` still has to be
// executed: 026 §2.4's unexecuted row keys on the plan's status, not on the
// section's outcome, so the descent must not be gated on `full`.
func TestTodoAcceptedPartialPlanStillNeedsExecuting(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n    coverage: partial\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n    coverage: partial\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"partial " + todoSpecRef + "#sec-1,sec-2 plan= task=",
		"unexecuted " + todoSpecRef + "#sec-1 plan=docs/plans/a.md task=WL-1",
	})
	if items[0].Detail != "2 sections are only partly covered, and no plan completes them" {
		t.Errorf("collapsed partial Detail = %q; want the plural form", items[0].Detail)
	}
}

// The two collapsed gaps order unplanned before partial.
func TestTodoUnplannedPrecedesPartial(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n    coverage: partial\n" +
				"---\n# A\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: closedSet("WL-1")})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	checkItems(t, items, []string{
		"unplanned " + todoSpecRef + "#sec-2 plan= task=",
		"partial " + todoSpecRef + "#sec-1 plan= task=",
	})
	// The CLI prints Detail verbatim, so it has to read correctly at one
	// section as well as at fifty.
	if items[0].Detail != "1 section has no covering plan" {
		t.Errorf("unplanned Detail = %q", items[0].Detail)
	}
	if items[1].Detail != "1 section is only partly covered, and no plan completes it" {
		t.Errorf("partial Detail = %q", items[1].Detail)
	}
}

// A draft plan claiming `full` suppresses the section's partial item as well
// as its unplanned one (026 §2.4): the pending act is accepting that plan.
func TestTodoDraftFullPlanSuppressesPartial(t *testing.T) {
	docs := buildTodoCorpus(t,
		map[string]string{"001-example.md": twoSectionSpec},
		map[string]string{
			"a.md": "---\nstatus: accepted\ntask: WL-1\ncovers:\n" +
				"  - spec: " + todoSpecRef + "#sec-1\n    coverage: partial\n" +
				"  - spec: " + todoSpecRef + "#sec-2\n    coverage: partial\n" +
				"---\n# A\n\nBody.\n",
			"b.md": "---\nstatus: draft\ncovers: " + todoSpecRef + "#sec-1\n---\n# B\n\nBody.\n",
		})
	items, _, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: closedSet("WL-1")})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	// sec-1's partial item is gone; only sec-2 remains in the collapsed gap.
	checkItems(t, items, []string{
		"partial " + todoSpecRef + "#sec-2 plan= task=",
		"plan-draft " + todoSpecRef + "#sec-1 plan=docs/plans/b.md task=",
	})
}

// One requires target written twice is one unfollowed edge, not two.
func TestTodoUnfollowedIsDeduplicated(t *testing.T) {
	spec := strings.Replace(twoSectionSpec, "issued: 2026-01-01",
		"issued: 2026-01-01\nrequires:\n- 002-bee.md\n- docs/specs/002-bee.md", 1)
	docs := buildTodoCorpus(t, map[string]string{
		"001-example.md": spec,
		"002-bee.md":     requiresSpecB,
	}, nil)
	_, diag, err := designdoc.Todo(docs, todoSpecRef, designdoc.TodoOptions{Closed: allKnownOpen})
	if err != nil {
		t.Fatalf("Todo: %v", err)
	}
	if len(diag.Unfollowed) != 1 {
		t.Errorf("Unfollowed = %v, want the repeated edge listed once", diag.Unfollowed)
	}
}
