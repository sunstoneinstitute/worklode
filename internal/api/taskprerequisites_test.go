package api

import (
	"bytes"
	"context"
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

func TestPrerequisitesView(t *testing.T) {
	v := prerequisitesView(prereqFixture(), "Root task")
	if v.Remaining != 5 || v.Direct != 2 {
		t.Errorf("remaining, direct = %d, %d; want 5, 2", v.Remaining, v.Direct)
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
	// 5 prerequisites plus the target; 8 distinct (ID, Via) edges.
	if len(v.Cards) != 6 || len(v.Links) != 8 {
		t.Errorf("cards, links = %d, %d; want 6, 8", len(v.Cards), len(v.Links))
	}
	x := map[string]int{}
	for _, c := range v.Cards {
		if _, dup := x[c.ID]; dup {
			t.Errorf("%s drawn twice", c.ID)
		}
		x[c.ID] = c.X
	}
	// Target pinned right; each column one step further left.
	if !(x["WL-1"] > x["WL-3"] && x["WL-3"] > x["WL-2"] && x["WL-2"] > x["WL-4"] && x["WL-4"] > x["WL-5"] && x["WL-5"] > x["WL-6"]) {
		t.Errorf("columns out of order: %v", x)
	}
	var cycleLinks int
	for _, l := range v.Links {
		if l.Cycle {
			cycleLinks++
		}
	}
	if cycleLinks != 1 {
		t.Errorf("cycle links = %d; want 1", cycleLinks)
	}
	if len(v.List) != 5 || v.List[0].ID != "WL-3" {
		t.Errorf("list = %+v; want 5 rows, nearest (WL-3) first", v.List)
	}
}

func TestPrerequisitesViewEmpty(t *testing.T) {
	if v := prerequisitesView(model.BlockerTree{Root: "WL-1"}, ""); v != nil {
		t.Errorf("no blockers: got %+v, want nil", v)
	}
	v := prerequisitesView(model.BlockerTree{Root: "WL-1", BlockingPlans: []model.DocRef{{ID: 1, Slug: "p"}}}, "")
	if v == nil || len(v.Plans) != 1 || len(v.Cards) != 0 {
		t.Errorf("plan only: got %+v, want one plan card and no drawing", v)
	}
}

func TestPrerequisitesViewCapsTheDrawing(t *testing.T) {
	tree := model.BlockerTree{Root: "WL-1"}
	for i := range prereqMaxCards + 5 {
		tree.Blockers = append(tree.Blockers, model.BlockerNode{ID: "WL-" + string(rune('A'+i%26)) + strings.Repeat("x", i/26), Via: "WL-1", Depth: 1})
	}
	v := prerequisitesView(tree, "")
	if v.Hidden != 5 || len(v.Cards) != prereqMaxCards+1 || len(v.List) != prereqMaxCards+5 {
		t.Errorf("hidden, cards, list = %d, %d, %d", v.Hidden, len(v.Cards), len(v.List))
	}
}

// TestTaskPageRendersPrerequisites renders the fixture through the task page:
// one card per task, the cycle and the plan named, the section collapsed, and
// no claim that nothing blocks the task.
func TestTaskPageRendersPrerequisites(t *testing.T) {
	view := ui.TaskView{
		Task:          model.Task{ID: "WL-1", Title: "Root task"},
		Prerequisites: prerequisitesView(prereqFixture(), "Root task"),
	}
	var buf bytes.Buffer
	if err := ui.Task(view).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if n := strings.Count(body, `<g class="pq-card`); n != 6 {
		t.Errorf("drawn cards = %d; want 6", n)
	}
	for _, id := range []string{"WL-2", "WL-3", "WL-4", "WL-5", "WL-6"} {
		if n := strings.Count(body, `<title>`+id+`: `); n != 1 {
			t.Errorf("%s drawn %d times; want once", id, n)
		}
	}
	for _, want := range []string{
		`<details class="card" id="prerequisites"`,
		"Prerequisites (5)",
		"Cycle: the prerequisites loop back through WL-4, WL-5",
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
