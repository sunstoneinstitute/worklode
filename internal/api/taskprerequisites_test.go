package api

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ui"
)

// prereqFixture is WL-1's blocker tree as store.BlockerTree returns it: WL-2
// and WL-3 are direct, WL-2 also reaches WL-1 through WL-3 (direct plus
// indirect), WL-4 is shared by WL-2 and WL-3, WL-4 and WL-5 depend on each
// other (a cycle), and WL-6 is the one task with nothing under it.
func prereqFixture() model.BlockerTree {
	return model.BlockerTree{
		Root: "WL-1",
		Blockers: []model.BlockerNode{
			{ID: "WL-2", Title: "Two", State: "ready", Via: "WL-1", Depth: 1},
			{ID: "WL-3", Title: "Three", State: "in_progress", Via: "WL-1", Depth: 1},
			{ID: "WL-2", Title: "Two", State: "ready", Via: "WL-3", Depth: 2},
			{ID: "WL-4", Title: "Four, a shared prerequisite with a long title", State: "ready", Via: "WL-2", Depth: 2},
			{ID: "WL-4", Title: "Four, a shared prerequisite with a long title", State: "ready", Via: "WL-3", Depth: 2},
			{ID: "WL-5", Title: "Five", State: "ready", Via: "WL-4", Depth: 3},
			{ID: "WL-4", Title: "Four, a shared prerequisite with a long title", State: "ready", Via: "WL-5", Depth: 4, Cycle: true},
			{ID: "WL-6", Title: "Six", State: "ready", Via: "WL-5", Depth: 4},
		},
		BlockingPlans: []model.DocRef{
			{ID: 7, Slug: "draft-plan", Title: "A draft plan with no tasks", Status: "draft"},
			{ID: 7, Slug: "draft-plan", Title: "A draft plan with no tasks", Status: "draft"},
		},
	}
}

// flatten walks the tree depth-first, counting full cards and references.
func flatten(nodes []ui.PrerequisiteNode, full map[string]ui.PrerequisiteNode, refs *[]ui.PrerequisiteNode) {
	for _, n := range nodes {
		if n.Ref {
			*refs = append(*refs, n)
			continue
		}
		full[n.ID] = n
		flatten(n.Children, full, refs)
	}
}

func TestPrerequisitesView(t *testing.T) {
	v := prerequisitesView(prereqFixture())
	if v.Remaining != 5 || v.Direct != 2 {
		t.Errorf("remaining, direct = %d, %d; want 5, 2", v.Remaining, v.Direct)
	}
	if v.Levels != 4 || v.Below != 5 {
		t.Errorf("target levels, below = %d, %d; want 4, 5", v.Levels, v.Below)
	}
	if !slices.Equal(v.Candidates, []string{"WL-6"}) {
		t.Errorf("candidates = %v; want [WL-6]", v.Candidates)
	}
	if !slices.Equal(v.Cycle, []string{"WL-4", "WL-5"}) {
		t.Errorf("cycle = %v; want [WL-4 WL-5]", v.Cycle)
	}
	if len(v.Plans) != 1 || v.Plans[0].URL != "/docs/7" {
		t.Errorf("plans = %+v; want one, linked /docs/7", v.Plans)
	}

	// The default view is the target's direct prerequisites.
	if len(v.Tree) != 2 || v.Tree[0].ID != "WL-2" || v.Tree[1].ID != "WL-3" {
		t.Fatalf("top level = %+v; want WL-2, WL-3", v.Tree)
	}
	full := map[string]ui.PrerequisiteNode{}
	var refs []ui.PrerequisiteNode
	flatten(v.Tree, full, &refs)
	if len(full) != 5 {
		t.Errorf("full cards = %d; want each of the 5 tasks once", len(full))
	}
	for id, want := range map[string][2]int{"WL-2": {3, 3}, "WL-3": {3, 4}, "WL-4": {2, 2}, "WL-5": {1, 2}, "WL-6": {0, 0}} {
		if n := full[id]; n.Levels != want[0] || n.Below != want[1] {
			t.Errorf("%s levels, below = %d, %d; want %d, %d", id, n.Levels, n.Below, want[0], want[1])
		}
	}
	// WL-4 is drawn at its shallowest position, under WL-2.
	if kids := full["WL-2"].Children; len(kids) != 1 || kids[0].ID != "WL-4" || kids[0].Ref {
		t.Errorf("WL-2 children = %+v; want WL-4 in full", kids)
	}
	// WL-3 holds the direct-plus-indirect WL-2 and the shared WL-4 as
	// references; WL-5's edge back to WL-4 is the one that closes the cycle.
	var got []string
	for _, r := range refs {
		got = append(got, fmt.Sprintf("%s cycle=%v", r.ID, r.Cycle))
	}
	slices.Sort(got)
	if want := []string{"WL-2 cycle=false", "WL-4 cycle=false", "WL-4 cycle=true"}; !slices.Equal(got, want) {
		t.Errorf("refs = %v; want %v", got, want)
	}
	if len(v.List) != 5 || v.List[0].ID != "WL-2" {
		t.Errorf("list = %+v; want 5 rows, nearest first", v.List)
	}
}

func TestPrerequisitesViewEmpty(t *testing.T) {
	if v := prerequisitesView(model.BlockerTree{Root: "WL-1"}); v != nil {
		t.Errorf("no blockers: got %+v, want nil", v)
	}
	v := prerequisitesView(model.BlockerTree{Root: "WL-1", BlockingPlans: []model.DocRef{{ID: 1, Slug: "p"}}})
	if v == nil || len(v.Plans) != 1 || len(v.Tree) != 0 {
		t.Errorf("plan only: got %+v, want one plan card and no tree", v)
	}
}

// A task that depends back on the target shows the target as a cycle
// reference and does not count it as a prerequisite.
func TestPrerequisitesViewCycleThroughTarget(t *testing.T) {
	v := prerequisitesView(model.BlockerTree{Root: "WL-1", Blockers: []model.BlockerNode{
		{ID: "WL-2", Via: "WL-1", Depth: 1},
		{ID: "WL-1", Via: "WL-2", Depth: 2, Cycle: true},
	}})
	if v.Remaining != 1 || len(v.Tree) != 1 || v.Tree[0].Below != 0 {
		t.Fatalf("got %+v", v)
	}
	if kids := v.Tree[0].Children; len(kids) != 1 || kids[0].ID != "WL-1" || !kids[0].Ref || !kids[0].Cycle {
		t.Errorf("WL-2 children = %+v; want a cycle reference to WL-1", kids)
	}
}

func TestPrerequisitesViewCapsTheTree(t *testing.T) {
	tree := model.BlockerTree{Root: "WL-1"}
	for i := range prereqMaxCards + 5 {
		tree.Blockers = append(tree.Blockers, model.BlockerNode{ID: fmt.Sprintf("WL-%d", i+2), Via: "WL-1", Depth: 1})
	}
	v := prerequisitesView(tree)
	if v.Hidden != 5 || len(v.Tree) != prereqMaxCards || len(v.List) != prereqMaxCards+5 {
		t.Errorf("hidden, tree, list = %d, %d, %d", v.Hidden, len(v.Tree), len(v.List))
	}
}

// TestTaskPageRendersPrerequisites renders the fixture through the task page:
// top-down, one full card per task, references linking to it, every level
// below the first collapsed, the cycle named, and no SVG.
func TestTaskPageRendersPrerequisites(t *testing.T) {
	view := ui.TaskView{
		Task:          model.Task{ID: "WL-1", Title: "Root task"},
		Prerequisites: prerequisitesView(prereqFixture()),
	}
	var buf bytes.Buffer
	if err := ui.Task(view).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	for _, id := range []string{"WL-1", "WL-2", "WL-3", "WL-4", "WL-5", "WL-6"} {
		if n := strings.Count(body, `id="pq-`+id+`"`); n != 1 {
			t.Errorf("%s has %d full cards; want 1", id, n)
		}
	}
	if n := strings.Count(body, `href="#pq-WL-4"`); n != 2 {
		t.Errorf("references to WL-4 = %d; want 2", n)
	}
	if strings.Contains(body, `id="pq-WL-2" open`) || strings.Contains(body, "pq-arrow") {
		t.Errorf("tree renders expanded or as SVG")
	}
	for _, want := range []string{
		`<details class="card" id="prerequisites"`,
		`<details class="pq-node" id="pq-WL-2">`,
		"Prerequisites (5)",
		"3 levels · 3 tasks below",
		"4 levels · 5 tasks below",
		"Cycle: the prerequisites loop back through WL-4, WL-5",
		`<span class="chip crit">cycle</span>`,
		"Candidates to start next:",
		"draft-plan",
		`class="pq-list"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, bad := range []string{"nothing blocks", "Nothing blocks", "claimable"} {
		if strings.Contains(body, bad) {
			t.Errorf("page says %q", bad)
		}
	}
}
