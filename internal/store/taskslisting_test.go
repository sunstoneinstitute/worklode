package store

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestListTasksFiltersAndOrdering(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "other", "Other", "OT"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}

	mk := func(project, priority string) *model.Task {
		in := defaultTaskInput()
		in.ProjectID = project
		in.Priority = priority
		return createTask(t, s, taskTestNow, in)
	}
	tLow := mk("horndb", "low")          // HDB-1
	tCrit := mk("horndb", "critical")    // HDB-2
	tMed := mk("horndb", "medium")       // HDB-3
	tHigh := mk("horndb", "high")        // HDB-4
	tCrit2 := mk("horndb", "critical")   // HDB-5
	tOther := mk("other", "high")        // OT-1
	walkTo(t, s, tMed.ID, "in_progress") // HDB-3 -> in_progress

	idsOf := func(tasks []model.Task) []string {
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return ids
	}

	// No filter: priority order (critical first), then id within a priority —
	// key lexically (HDB before OT), then the numeric suffix.
	all, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	wantAll := []string{tCrit.ID, tCrit2.ID, tHigh.ID, tOther.ID, tMed.ID, tLow.ID}
	if got := idsOf(all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("ListTasks order: got %v, want %v", got, wantAll)
	}

	// Project filter.
	horn, err := s.ListTasks(ctx, TaskFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("ListTasks project: %v", err)
	}
	wantHorn := []string{tCrit.ID, tCrit2.ID, tHigh.ID, tMed.ID, tLow.ID}
	if got := idsOf(horn); !reflect.DeepEqual(got, wantHorn) {
		t.Fatalf("ListTasks project=horndb: got %v, want %v", got, wantHorn)
	}

	// States filter.
	inProg, err := s.ListTasks(ctx, TaskFilter{States: []string{"in_progress"}})
	if err != nil {
		t.Fatalf("ListTasks states: %v", err)
	}
	if got := idsOf(inProg); !reflect.DeepEqual(got, []string{tMed.ID}) {
		t.Fatalf("ListTasks states=[in_progress]: got %v, want [%s]", got, tMed.ID)
	}

	// Priority filter.
	crit, err := s.ListTasks(ctx, TaskFilter{Priority: "critical"})
	if err != nil {
		t.Fatalf("ListTasks priority: %v", err)
	}
	if got := idsOf(crit); !reflect.DeepEqual(got, []string{tCrit.ID, tCrit2.ID}) {
		t.Fatalf("ListTasks priority=critical: got %v, want [%s %s]", got, tCrit.ID, tCrit2.ID)
	}

	// Combined filters.
	combo, err := s.ListTasks(ctx, TaskFilter{Project: "other", States: []string{"ready", "draft"}, Priority: "high"})
	if err != nil {
		t.Fatalf("ListTasks combined: %v", err)
	}
	if got := idsOf(combo); !reflect.DeepEqual(got, []string{tOther.ID}) {
		t.Fatalf("ListTasks combined: got %v, want [%s]", got, tOther.ID)
	}
}

// TestListTasksOrderMatchesModelCompareTaskIDs pins taskListOrder's SQL
// ORDER BY to model.CompareTaskIDs (061 §4 S3): a project key matches
// ^[A-Z][A-Z0-9]{1,9}$ and never contains '-', so split_part(id,'-',1) is
// always the key and CAST(split_part(id,'-',2) AS INTEGER) the numeric
// suffix, making the two orderings equivalent by construction. That argument
// held by inspection, not by test, until this pins it: a shuffled set
// spanning the boundary a plain lexical sort gets wrong ("10" < "2" as
// strings; "AB" < "WL" as keys), all one priority so the id tiebreak is what
// is under test.
func TestListTasksOrderMatchesModelCompareTaskIDs(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	ids := []string{"WL-10", "WL-2", "WL-9", "AB-100", "AB-9", "WL-1"}
	for _, id := range ids {
		insertBareTask(t, s, id)
	}

	got, err := s.ListTasks(ctx, TaskFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var gotIDs []string
	for _, task := range got {
		gotIDs = append(gotIDs, task.ID)
	}

	want := slices.SortedFunc(slices.Values(ids), model.CompareTaskIDs)
	if !slices.Equal(gotIDs, want) {
		t.Fatalf("ListTasks order = %v, want %v (model.CompareTaskIDs order)", gotIDs, want)
	}
}

// TestListTasksFilterByPlanDoc: TaskFilter.PlanDoc narrows to exactly the
// tasks minted from one plan document — the query that is the plan's task
// set (025 §9.2, §1). A task with no plan_doc is unaffected either way.
func TestListTasksFilterByPlanDoc(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	seedDocsProject(t, s)

	planA, err := insertDoc(t, s, "plan", 1, "plan-a")
	if err != nil {
		t.Fatalf("insert plan a: %v", err)
	}
	planB, err := insertDoc(t, s, "plan", 2, "plan-b")
	if err != nil {
		t.Fatalf("insert plan b: %v", err)
	}

	// PlanTaskKey travels with PlanDoc: a CHECK constraint holds the pair
	// together, because a minted task must say which declaration it covers
	// (025 §9.2).
	inA1 := defaultTaskInput()
	inA1.PlanDoc, inA1.PlanTaskKey = planA, "A first"
	a1 := createTask(t, s, taskTestNow, inA1)

	inA2 := defaultTaskInput()
	inA2.PlanDoc, inA2.PlanTaskKey = planA, "A second"
	a2 := createTask(t, s, taskTestNow, inA2)

	inB := defaultTaskInput()
	inB.PlanDoc, inB.PlanTaskKey = planB, "B first"
	b1 := createTask(t, s, taskTestNow, inB)

	unplanned := createTask(t, s, taskTestNow, defaultTaskInput())

	idsOf := func(tasks []model.Task) []string {
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return ids
	}
	sortedIDs := func(ids ...string) []string {
		slices.Sort(ids)
		return ids
	}

	gotA, err := s.ListTasks(ctx, TaskFilter{PlanDoc: planA})
	if err != nil {
		t.Fatalf("ListTasks plan_doc=A: %v", err)
	}
	if got := sortedIDs(idsOf(gotA)...); !reflect.DeepEqual(got, sortedIDs(a1.ID, a2.ID)) {
		t.Fatalf("ListTasks plan_doc=A: got %v, want [%s %s]", got, a1.ID, a2.ID)
	}

	gotB, err := s.ListTasks(ctx, TaskFilter{PlanDoc: planB})
	if err != nil {
		t.Fatalf("ListTasks plan_doc=B: %v", err)
	}
	if got := idsOf(gotB); !reflect.DeepEqual(got, []string{b1.ID}) {
		t.Fatalf("ListTasks plan_doc=B: got %v, want [%s]", got, b1.ID)
	}

	all, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks unfiltered: %v", err)
	}
	if got := sortedIDs(idsOf(all)...); !reflect.DeepEqual(got, sortedIDs(a1.ID, a2.ID, b1.ID, unplanned.ID)) {
		t.Fatalf("ListTasks unfiltered: got %v, want all four tasks", got)
	}
}

// TestListTasksFilterByAboutDoc mirrors TestListTasksFilterByPlanDoc:
// TaskFilter.AboutDoc narrows to exactly the tasks referencing one document.
func TestListTasksFilterByAboutDoc(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()
	seedDocsProject(t, s)

	docA, err := insertDoc(t, s, "spec", 27, "spec-27")
	if err != nil {
		t.Fatalf("insert doc a: %v", err)
	}
	docB, err := insertDoc(t, s, "spec", 28, "spec-28")
	if err != nil {
		t.Fatalf("insert doc b: %v", err)
	}

	inA1 := defaultTaskInput()
	inA1.AboutDoc = docA
	a1 := createTask(t, s, taskTestNow, inA1)

	inA2 := defaultTaskInput()
	inA2.AboutDoc = docA
	a2 := createTask(t, s, taskTestNow, inA2)

	inB := defaultTaskInput()
	inB.AboutDoc = docB
	b1 := createTask(t, s, taskTestNow, inB)

	unrelated := createTask(t, s, taskTestNow, defaultTaskInput())

	idsOf := func(tasks []model.Task) []string {
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return ids
	}
	sortedIDs := func(ids ...string) []string {
		slices.Sort(ids)
		return ids
	}

	gotA, err := s.ListTasks(ctx, TaskFilter{AboutDoc: docA})
	if err != nil {
		t.Fatalf("ListTasks about_doc=A: %v", err)
	}
	if got := sortedIDs(idsOf(gotA)...); !reflect.DeepEqual(got, sortedIDs(a1.ID, a2.ID)) {
		t.Fatalf("ListTasks about_doc=A: got %v, want [%s %s]", got, a1.ID, a2.ID)
	}

	gotB, err := s.ListTasks(ctx, TaskFilter{AboutDoc: docB})
	if err != nil {
		t.Fatalf("ListTasks about_doc=B: %v", err)
	}
	if got := idsOf(gotB); !reflect.DeepEqual(got, []string{b1.ID}) {
		t.Fatalf("ListTasks about_doc=B: got %v, want [%s]", got, b1.ID)
	}

	all, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks unfiltered: %v", err)
	}
	if got := sortedIDs(idsOf(all)...); !reflect.DeepEqual(got, sortedIDs(a1.ID, a2.ID, b1.ID, unrelated.ID)) {
		t.Fatalf("ListTasks unfiltered: got %v, want all four tasks", got)
	}
}

// TestListTasksFilterByUpdatedSince covers the incremental sync path: a
// client (the Obsidian mirror) re-asks for what changed since the highest
// updated_at it has seen, and gets that boundary row back with it.
func TestListTasksFilterByUpdatedSince(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	older := createTask(t, s, taskTestNow, defaultTaskInput())
	cut := taskTestNow.Add(time.Hour)
	atCut := createTask(t, s, cut, defaultTaskInput())
	later := createTask(t, s, cut.Add(time.Minute), defaultTaskInput())

	idsOf := func(tasks []model.Task) []string {
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return ids
	}

	// >=, not >: the row sitting exactly on the watermark comes back.
	changed, err := s.ListTasks(ctx, TaskFilter{UpdatedSince: cut})
	if err != nil {
		t.Fatalf("ListTasks updated_since: %v", err)
	}
	if got := idsOf(changed); !reflect.DeepEqual(got, []string{atCut.ID, later.ID}) {
		t.Fatalf("ListTasks updated_since=cut: got %v, want [%s %s]", got, atCut.ID, later.ID)
	}

	// The zero value does not filter.
	all, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	if got := idsOf(all); !reflect.DeepEqual(got, []string{older.ID, atCut.ID, later.ID}) {
		t.Fatalf("ListTasks zero filter: got %v, want all three", got)
	}
}

// TestListTasksByRepo: a client running inside a checkout knows its repo, not
// the project id, so the repo it is in must be a usable key for "which tasks
// could this merge advance".
func TestListTasksByRepo(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "other", "Other", "OT"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if err := s.AddRepo(ctx, "horndb", "acme/horndb"); err != nil {
		t.Fatalf("AddRepo horndb: %v", err)
	}
	if err := s.AddRepo(ctx, "other", "acme/other"); err != nil {
		t.Fatalf("AddRepo other: %v", err)
	}

	mk := func(project string) *model.Task {
		in := defaultTaskInput()
		in.ProjectID = project
		return createTask(t, s, taskTestNow, in)
	}
	mine := mk("horndb")
	mk("other")

	got, err := s.ListTasks(ctx, TaskFilter{Repo: "acme/horndb"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("repo filter returned %+v, want only %s", got, mine.ID)
	}

	// An unmapped repo is empty, not everything: a filter that silently
	// stops filtering would report another project's tasks as candidates.
	got, err = s.ListTasks(ctx, TaskFilter{Repo: "acme/nope"})
	if err != nil {
		t.Fatalf("ListTasks unmapped: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unmapped repo returned %+v, want none", got)
	}
}
