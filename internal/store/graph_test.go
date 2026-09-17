package store

import (
	"testing"
	"time"
)

// TestProjectGraph: one read returns the project's live tasks and task edges,
// and only the documents a task reaches — directly or over doc edges.
func TestProjectGraph(t *testing.T) {
	t.Parallel()
	s := openDocStore(t) // project p1 and actors stig/ada seeded (docs_test.go)
	specID, planID, taskIDs := seedProgressCorpus(t, s)

	// A second spec nothing links to: must be left out.
	orphan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 67, Slug: "067-orphan",
		Body: "---\nstatus: draft\n---\n\n# Orphan\n\n## 0. A {#sec-0}\n\nA.\n", CreatedBy: "stig",
	})
	// An ADR reachable only through a doc edge: it amends spec 066's sec-0,
	// and nothing else points at it.
	adr := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "adr", Number: 1, Slug: "001-adr",
		Body: "---\nstatus: draft\namends:\n  \"#sec-0\": 066-progress.md#sec-0\n---\n\n# ADR\n\n## 0. A {#sec-0}\n\nA.\n", CreatedBy: "stig",
	})

	// A standalone task blocked by a minted one.
	loose := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "Loose", Priority: "low", Kind: "chore", CreatedBy: "stig"})
	if err := addEdge(t, s, taskIDs[0], loose.ID, "blocks"); err != nil {
		t.Fatal(err)
	}

	g, err := s.ProjectGraph(t.Context(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Project != "p1" {
		t.Errorf("project = %q", g.Project)
	}
	ids := map[string]bool{}
	for _, task := range g.Tasks {
		ids[task.ID] = true
		if task.Body != "" {
			t.Errorf("task %s carries a body", task.ID)
		}
	}
	for _, id := range append(taskIDs, loose.ID) {
		if !ids[id] {
			t.Errorf("task %s missing from graph", id)
		}
	}
	docIDs := map[int64]bool{}
	for _, d := range g.Docs {
		docIDs[d.ID] = true
		if d.Body != "" {
			t.Errorf("doc %d carries a body", d.ID)
		}
	}
	if !docIDs[planID] || !docIDs[specID] {
		t.Errorf("plan %d and spec %d must be reachable; got %v", planID, specID, docIDs)
	}
	if docIDs[orphan.ID] {
		t.Errorf("orphan doc %d must be left out", orphan.ID)
	}
	if !docIDs[adr.ID] {
		t.Errorf("adr %d reachable through the spec must be kept", adr.ID)
	}
	var sawPlanned, sawCovers, sawBlocks bool
	for _, l := range g.Links {
		if l.Type == "planned_in" && l.Task == taskIDs[0] && l.Doc == planID {
			sawPlanned = true
		}
	}
	for _, e := range g.DocEdges {
		if e.Type == "covers" && e.From == planID && e.To == specID {
			sawCovers = true
		}
	}
	for _, e := range g.TaskEdges {
		if e.Type == "blocks" && e.From == taskIDs[0] && e.To == loose.ID {
			sawBlocks = true
		}
	}
	if !sawPlanned || !sawCovers || !sawBlocks {
		t.Errorf("planned_in=%v covers=%v blocks=%v", sawPlanned, sawCovers, sawBlocks)
	}
	// covers is section-scoped in doc_edges (three rows for three sections);
	// the graph collapses them to one.
	n := 0
	for _, e := range g.DocEdges {
		if e.Type == "covers" && e.From == planID && e.To == specID {
			n++
		}
	}
	if n != 1 {
		t.Errorf("covers edges plan->spec = %d, want 1", n)
	}
}

func TestProjectGraphEmptyProjectHasEmptySlices(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	g, err := s.ProjectGraph(t.Context(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Tasks == nil || g.TaskEdges == nil || g.Docs == nil || g.DocEdges == nil || g.Links == nil {
		t.Errorf("slices must be non-nil: %+v", g)
	}
}
