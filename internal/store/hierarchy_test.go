package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMigrationAllowsEpicKind checks the 0006 CHECK change: 'epic' is a legal
// task kind and an unknown kind is still rejected.
func TestMigrationAllowsEpicKind(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	if epic.Kind != "epic" {
		t.Fatalf("kind = %q, want epic", epic.Kind)
	}

	bad := defaultTaskInput()
	bad.Kind = "saga"
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, _ int64) error {
			_, err := CreateTask(tx, taskTestNow, bad)
			return err
		})
	if !isCheckViolationOn(err, "tasks_kind_check") {
		t.Fatalf("error = %v, want a tasks_kind_check violation", err)
	}
}

// TestSingleParentIndex checks that the partial unique index rejects a second
// child_of edge out of one task, whichever parent it points at.
func TestSingleParentIndex(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	epicA := createTask(t, s, taskTestNow, epicInput())
	epicB := createTask(t, s, taskTestNow, epicInput())

	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, epicA.ID, taskTestNow); err != nil {
		t.Fatalf("first parent edge: %v", err)
	}
	_, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, epicB.ID, taskTestNow)
	if err == nil {
		t.Fatal("second parent edge succeeded, want a unique violation")
	}
	if !isUniqueViolationOn(err, "task_edges_single_parent") {
		t.Fatalf("error = %v, want a task_edges_single_parent unique violation", err)
	}
}

// TestUpMigrationDedupsDuplicateParentEdges checks that 0006's DELETE step
// drops all but the latest child_of edge out of a task before the
// single-parent index is created, so a database carrying pre-existing
// duplicates (legal under 0005) migrates cleanly instead of aborting with a
// unique violation.
func TestUpMigrationDedupsDuplicateParentEdges(t *testing.T) {
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(migrationsThrough(t, 5)); err != nil {
		t.Fatalf("migrate through 0005: %v", err)
	}
	if err := s.CreateProject(t.Context(), "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.CreateActor(t.Context(), "stig", "human", "Stig", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}

	// Insert rows directly rather than through CreateTask: the store is
	// deliberately held at 0005 here, and CreateTask writes columns later
	// migrations add.
	insertTaskAt0005 := func(id string) {
		t.Helper()
		if _, err := s.DBForTests().Exec(
			`INSERT INTO tasks (id, project_id, title, body, priority, kind, state, created_by, created_at, updated_at)
			 VALUES ($1, 'horndb', 'a task', 'body', 'medium', 'feature', 'ready', 'stig', $2, $2)`,
			id, taskTestNow); err != nil {
			t.Fatalf("insert task %s: %v", id, err)
		}
	}
	childID, parentAID, parentBID := "HDB-1", "HDB-2", "HDB-3"
	insertTaskAt0005(childID)
	insertTaskAt0005(parentAID)
	insertTaskAt0005(parentBID)

	older, newer := taskTestNow, taskTestNow.Add(time.Hour)
	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		childID, parentAID, older); err != nil {
		t.Fatalf("insert first parent edge: %v", err)
	}
	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		childID, parentBID, newer); err != nil {
		t.Fatalf("insert second parent edge: %v", err)
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}

	rows, err := s.DBForTests().Query(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`, childID)
	if err != nil {
		t.Fatalf("query surviving edges: %v", err)
	}
	defer rows.Close()
	var survivors []string
	for rows.Next() {
		var to string
		if err := rows.Scan(&to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		survivors = append(survivors, to)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(survivors) != 1 || survivors[0] != parentBID {
		t.Fatalf("surviving child_of edges = %v, want [%s] (the later one)", survivors, parentBID)
	}
}

// migrationsThrough copies the up/down pairs through version n out of
// MigrationsDirForTests into a fresh temp dir, so a test can migrate a store
// to an intermediate schema version before seeding data and applying the
// rest.
func migrationsThrough(t *testing.T, n int) string {
	t.Helper()
	src := MigrationsDirForTests()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	dir := t.TempDir()
	for _, e := range entries {
		var version int
		if _, err := fmt.Sscanf(e.Name(), "%04d_", &version); err != nil || version > n {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	return dir
}

// epicInput is the shared fixture for a container task.
func epicInput() TaskInput {
	in := defaultTaskInput()
	in.Title = "an epic"
	in.Kind = "epic"
	return in
}

func TestAddEdgeRejectsNonEpicParent(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parent := createTask(t, s, taskTestNow, defaultTaskInput())

	err := addEdge(t, s, child.ID, parent.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestAddEdgeRejectsSecondParent(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	epicA := createTask(t, s, taskTestNow, epicInput())
	epicB := createTask(t, s, taskTestNow, epicInput())

	if err := addEdge(t, s, child.ID, epicA.ID, "child_of"); err != nil {
		t.Fatalf("first parent: %v", err)
	}
	err := addEdge(t, s, child.ID, epicB.ID, "child_of")
	if !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("error = %v, want ErrEdgeExists", err)
	}
	// The baseline duplicate-edge rule still applies to the same pair.
	if err := addEdge(t, s, child.ID, epicA.ID, "child_of"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge error = %v, want ErrEdgeExists", err)
	}
}

func TestAddEdgeRejectsCrossProject(t *testing.T) {
	s := openTaskStore(t)
	if err := s.CreateProject(t.Context(), "other", "Other", "OTH"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic := createTask(t, s, taskTestNow, epicInput())
	otherIn := defaultTaskInput()
	otherIn.ProjectID = "other"
	child := createTask(t, s, taskTestNow, otherIn)

	err := addEdge(t, s, child.ID, epic.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestAddEdgeEnforcesDepthCap(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, epicInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	deep := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, mid.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("depth 1: %v", err)
	}
	if err := addEdge(t, s, leaf.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("depth 2: %v", err)
	}
	// A third level is one edge too many.
	err := addEdge(t, s, deep.ID, leaf.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// TestAddEdgeDepthCapCountsSubtree checks that adopting a task that already
// has children counts the whole resulting chain, not just the new edge.
func TestAddEdgeDepthCapCountsSubtree(t *testing.T) {
	s := openTaskStore(t)
	top := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, epicInput())
	sub := createTask(t, s, taskTestNow, epicInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, leaf.ID, sub.ID, "child_of"); err != nil {
		t.Fatalf("leaf under sub: %v", err)
	}
	if err := addEdge(t, s, sub.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("sub under mid: %v", err)
	}
	// mid already carries a 2-deep subtree; hanging it under top makes 3.
	err := addEdge(t, s, mid.ID, top.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// TestAddEdgeStillRejectsCycles checks the pre-existing cycle guard survives
// the rewrite: a parent cannot become a child of its own descendant.
func TestAddEdgeStillRejectsCycles(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, epicInput())

	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child under epic: %v", err)
	}
	err := addEdge(t, s, epic.ID, child.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("error = %v, want ErrCycle", err)
	}
}

// TestDescendantDepthWideFanOut checks that a wide, shallow fan-out (many
// direct children sharing one parent) is computed with the batched ANY($1)
// query and still reports depth 1: width alone must not be mistaken for
// depth.
func TestDescendantDepthWideFanOut(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, epicInput())
	for i := 0; i < 25; i++ {
		leaf := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, leaf.ID, parent.ID, "child_of"); err != nil {
			t.Fatalf("fan-out child %d: %v", i, err)
		}
	}

	var depth int
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		var err error
		depth, err = descendantDepth(tx, parent.ID)
		return err
	}); err != nil {
		t.Fatalf("descendantDepth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("depth = %d, want 1 (25 direct children, one level deep)", depth)
	}
}

// TestDescendantDepthStopsAtCap pins the early-exit behavior: a chain that
// runs deeper than maxHierarchyDepth+1 must not be walked past the point
// where the cap is already known to be blown. The fixture is built with
// direct inserts (bypassing AddEdge, which would refuse a chain this deep)
// so the walk has a level to stop short of.
func TestDescendantDepthStopsAtCap(t *testing.T) {
	s := openTaskStore(t)
	top := createTask(t, s, taskTestNow, epicInput())
	n1 := createTask(t, s, taskTestNow, epicInput())
	n2 := createTask(t, s, taskTestNow, epicInput())
	n3 := createTask(t, s, taskTestNow, epicInput())
	n4 := createTask(t, s, taskTestNow, defaultTaskInput())

	// top <- n1 <- n2 <- n3 <- n4: 4 levels below top, two past the cap.
	for _, e := range []struct{ child, parent string }{
		{n1.ID, top.ID}, {n2.ID, n1.ID}, {n3.ID, n2.ID}, {n4.ID, n3.ID},
	} {
		if _, err := s.DBForTests().Exec(
			`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
			e.child, e.parent, taskTestNow); err != nil {
			t.Fatalf("insert %s child_of %s: %v", e.child, e.parent, err)
		}
	}

	var depth int
	if err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		var err error
		depth, err = descendantDepth(tx, top.ID)
		return err
	}); err != nil {
		t.Fatalf("descendantDepth: %v", err)
	}
	// The true chain is 4 deep; a walk that doesn't stop early would return 4.
	if depth != maxHierarchyDepth+1 {
		t.Fatalf("depth = %d, want %d (walk should stop one level past the cap)", depth, maxHierarchyDepth+1)
	}
}

func TestChildProgress(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	var kids []*Task
	for i := 0; i < 3; i++ {
		k := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, k.ID, epic.ID, "child_of"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		kids = append(kids, k)
	}
	// A blocks edge into the epic shares child_of's direction (to_task =
	// epic). It must not be counted: pins the e.type = 'child_of' predicate.
	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, epic.ID, "blocks"); err != nil {
		t.Fatalf("blocks: %v", err)
	}

	got, err := s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{Closed: 0, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}

	walkTo(t, s, kids[0].ID, "merged")
	walkTo(t, s, kids[1].ID, "abandoned")
	got, err = s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{Closed: 2, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestChildProgressNoChildren(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	got, err := s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestParentOf(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	got, err := s.ParentOf(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("ParentOf: %v", err)
	}
	if got == nil || got.ID != epic.ID || got.Title != epic.Title || got.State != epic.State {
		t.Fatalf("parent = %+v, want id/title/state of %s", got, epic.ID)
	}

	root, err := s.ParentOf(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ParentOf root: %v", err)
	}
	if root != nil {
		t.Fatalf("parent of a root task = %+v, want nil", root)
	}
}

// TestParentMap covers both the scoped (projectID != "") and unscoped
// (projectID == "") branches, across two projects.
func TestParentMap(t *testing.T) {
	s := openTaskStore(t)
	if err := s.CreateProject(t.Context(), "other", "Other", "OTH"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	otherEpicIn := epicInput()
	otherEpicIn.ProjectID = "other"
	otherEpic := createTask(t, s, taskTestNow, otherEpicIn)
	otherChildIn := defaultTaskInput()
	otherChildIn.ProjectID = "other"
	otherChild := createTask(t, s, taskTestNow, otherChildIn)
	if err := addEdge(t, s, otherChild.ID, otherEpic.ID, "child_of"); err != nil {
		t.Fatalf("other child_of: %v", err)
	}

	scoped, err := s.ParentMap(t.Context(), "horndb")
	if err != nil {
		t.Fatalf("ParentMap horndb: %v", err)
	}
	if want := map[string]string{child.ID: epic.ID}; !reflect.DeepEqual(scoped, want) {
		t.Fatalf("ParentMap(horndb) = %v, want %v", scoped, want)
	}

	all, err := s.ParentMap(t.Context(), "")
	if err != nil {
		t.Fatalf("ParentMap all: %v", err)
	}
	if want := (map[string]string{child.ID: epic.ID, otherChild.ID: otherEpic.ID}); !reflect.DeepEqual(all, want) {
		t.Fatalf("ParentMap(\"\") = %v, want %v", all, want)
	}
}

func TestListTasksFilterParentAndKind(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	loose := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	kids, err := s.ListTasks(t.Context(), TaskFilter{Parent: epic.ID})
	if err != nil {
		t.Fatalf("ListTasks parent: %v", err)
	}
	if len(kids) != 1 || kids[0].ID != child.ID {
		t.Fatalf("children = %v, want [%s]", taskIDs(kids), child.ID)
	}
	if slices.Contains(taskIDs(kids), loose.ID) {
		t.Fatalf("children = %v, must not include the parentless task %s", taskIDs(kids), loose.ID)
	}

	epics, err := s.ListTasks(t.Context(), TaskFilter{Kind: "epic"})
	if err != nil {
		t.Fatalf("ListTasks kind: %v", err)
	}
	if len(epics) != 1 || epics[0].ID != epic.ID {
		t.Fatalf("epics = %v, want [%s]", taskIDs(epics), epic.ID)
	}
	if slices.Contains(taskIDs(epics), loose.ID) {
		t.Fatalf("epics = %v, must not include the non-epic task %s", taskIDs(epics), loose.ID)
	}
}

func taskIDs(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

// TestEpicNeverInReadySet checks that an epic stays out of the ranked pickup
// set even when it is ready, unblocked, and top-ranked by every other factor.
func TestEpicNeverInReadySet(t *testing.T) {
	s := openTaskStore(t)
	in := epicInput()
	in.Priority = "critical"
	epic := createTask(t, s, taskTestNow, in)
	plain := createTask(t, s, taskTestNow, defaultTaskInput())

	got, err := s.readyCandidates(t.Context(), "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	if len(got) != 1 || got[0].ID != plain.ID {
		t.Fatalf("candidates = %v, want [%s] (epic %s excluded)", taskIDs(got), plain.ID, epic.ID)
	}
}

// TestClaimRejectsEpic checks the direct-claim hole: ready -> in_progress is a
// legal epic transition (it is the roll-up trigger), so Claim needs its own
// guard.
func TestClaimRejectsEpic(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, err := s.Claim(t.Context(), epic.ID, "stig", "wt-1", time.Hour)
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("error = %v, want ErrBadTransition", err)
	}
}

// TestEpicForbiddenStates checks the kind guard on both ends of a transition:
// an epic can never enter a delivery state, and `lode task done` (in_review ->
// merged) reports the roll-up rule rather than a from-state mismatch.
func TestEpicForbiddenStates(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	if err := transition(t, s, taskTestNow, epic.ID, "ready", "in_progress"); err != nil {
		t.Fatalf("ready -> in_progress: %v", err)
	}
	err := transition(t, s, taskTestNow, epic.ID, "in_progress", "in_review")
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("in_review error = %v, want ErrBadTransition", err)
	}
	if !strings.Contains(err.Error(), "epic") {
		t.Fatalf("error %q does not name the epic rule", err)
	}
	err = transition(t, s, taskTestNow, epic.ID, "in_review", "merged")
	if !errors.Is(err, ErrBadTransition) || !strings.Contains(err.Error(), "epic") {
		t.Fatalf("done error = %v, want an epic ErrBadTransition", err)
	}
	// The roll-up terminal is still reachable.
	if err := transition(t, s, taskTestNow, epic.ID, "in_progress", "merged"); err != nil {
		t.Fatalf("in_progress -> merged: %v", err)
	}
}

// TestResolveDeliveryIgnoresEpics checks that an epic with commit and deploy
// facts attributed to it is left alone.
func TestResolveDeliveryIgnoresEpics(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "delivery", nil,
		func(tx *sql.Tx, eventID int64) error {
			if err := InsertTaskCommit(tx, TaskCommit{
				TaskID: epic.ID, Repo: "o/r", SHA: "abc", Source: "branch_push", SeenAt: taskTestNow,
			}); err != nil {
				return err
			}
			if _, err := AppendMainCommit(tx, "o/r", "abc", taskTestNow); err != nil {
				return err
			}
			return ResolveDelivery(tx, taskTestNow, epic.ID, "o/r", eventID)
		})
	if err != nil {
		t.Fatalf("ResolveDelivery: %v", err)
	}
	got, err := s.GetTask(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state = %s, want ready (an epic has no commit)", got.State)
	}
}

func TestEpicTarget(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		want   string
	}{
		{"no children", nil, ""},
		{"all draft or ready", []string{"draft", "ready"}, "ready"},
		{"one started", []string{"ready", "in_progress"}, "in_progress"},
		{"one in review", []string{"ready", "in_review"}, "in_progress"},
		{"one landed, one open", []string{"merged", "ready"}, "in_progress"},
		{"all closed, one delivered", []string{"merged", "abandoned"}, "merged"},
		{"all closed via deploy", []string{"deployed_dev", "released"}, "merged"},
		{"all abandoned", []string{"abandoned", "abandoned"}, "abandoned"},
		{"all delivered", []string{"merged", "deployed_prod"}, "merged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := epicTarget(tc.states); got != tc.want {
				t.Fatalf("epicTarget(%v) = %q, want %q", tc.states, got, tc.want)
			}
		})
	}
}

// epicWithChildren builds an epic with n ready children and returns both.
func epicWithChildren(t *testing.T, s *Store, n int) (*Task, []*Task) {
	t.Helper()
	epic := createTask(t, s, taskTestNow, epicInput())
	var kids []*Task
	for i := 0; i < n; i++ {
		k := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, k.ID, epic.ID, "child_of"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		kids = append(kids, k)
	}
	return epic, kids
}

func stateOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	task, err := s.GetTask(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTask %s: %v", id, err)
	}
	return task.State
}

// TestRollUpForward: the first child to start moves the epic off ready, and
// the last child to close moves it to merged.
func TestRollUpForward(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)

	if err := transition(t, s, taskTestNow, kids[0].ID, "ready", "in_progress"); err != nil {
		t.Fatalf("start child 0: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "in_progress" {
		t.Fatalf("epic = %s, want in_progress", got)
	}

	// child 0 is already in in_progress and walkTo always restarts from ready,
	// so step it manually.
	if err := transition(t, s, taskTestNow, kids[0].ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("review child 0: %v", err)
	}
	if err := transition(t, s, taskTestNow, kids[0].ID, "in_review", "merged"); err != nil {
		t.Fatalf("merge child 0: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "in_progress" {
		t.Fatalf("epic = %s, want in_progress with one child still open", got)
	}
	walkTo(t, s, kids[1].ID, "merged")
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}
}

// TestRollUpZeroChildren: an epic with no children never moves. It is a
// modelling mistake, not a completed epic.
func TestRollUpZeroChildren(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "rollup", nil,
		func(tx *sql.Tx, eventID int64) error {
			return ResolveHierarchy(tx, taskTestNow, epic.ID, eventID)
		})
	if err != nil {
		t.Fatalf("ResolveHierarchy: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "ready" {
		t.Fatalf("epic = %s, want ready", got)
	}
}

func TestRollUpAllAbandoned(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)
	for _, k := range kids {
		if err := transition(t, s, taskTestNow, k.ID, "ready", "abandoned"); err != nil {
			t.Fatalf("abandon %s: %v", k.ID, err)
		}
	}
	if got := stateOf(t, s, epic.ID); got != "abandoned" {
		t.Fatalf("epic = %s, want abandoned", got)
	}
}

func TestRollUpMixedAbandonedAndDelivered(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)
	walkTo(t, s, kids[0].ID, "merged")
	if err := transition(t, s, taskTestNow, kids[1].ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged (some of the epic landed)", got)
	}
}

// TestRollUpReopen: a child returning to ready puts a closed epic back to
// ready. Asymmetric roll-up produces boards that lie.
func TestRollUpReopen(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 1)
	walkTo(t, s, kids[0].ID, "merged")
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}
	if err := transition(t, s, taskTestNow, kids[0].ID, "merged", "ready"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "ready" {
		t.Fatalf("epic = %s, want ready", got)
	}
}

// TestRollUpAttribution: the parent's state_log row carries the child's event
// id, so the timeline explains itself with no synthetic event.
func TestRollUpAttribution(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 1)

	var eventID int64
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, id int64) error {
			eventID = id
			return Transition(tx, taskTestNow, kids[0].ID, "ready", "in_progress", id)
		})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}

	var got int64
	if err := s.DBForTests().QueryRow(
		`SELECT event_id FROM state_log WHERE entity_id = $1 ORDER BY id DESC LIMIT 1`,
		epic.ID).Scan(&got); err != nil {
		t.Fatalf("read epic state_log: %v", err)
	}
	if got != eventID {
		t.Fatalf("epic state_log event_id = %d, want the child's %d", got, eventID)
	}
}

// TestRollUpDepth2Recursion: a subtask closing resolves its task, which
// resolves the epic, in one transaction.
func TestRollUpDepth2Recursion(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, epicInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("mid under epic: %v", err)
	}
	if err := addEdge(t, s, leaf.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("leaf under mid: %v", err)
	}

	walkTo(t, s, leaf.ID, "merged")
	if got := stateOf(t, s, mid.ID); got != "merged" {
		t.Fatalf("mid = %s, want merged", got)
	}
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}
}

// TestRollUpReopenRoutesThroughReady: merged -> in_progress is not a legal
// edge, so a merged epic whose child reopens while another stays closed
// routes through ready, the only edge out of a closed state.
func TestRollUpReopenRoutesThroughReady(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)
	walkTo(t, s, kids[0].ID, "merged")
	walkTo(t, s, kids[1].ID, "abandoned")
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}

	if err := transition(t, s, taskTestNow, kids[0].ID, "merged", "ready"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "in_progress" {
		t.Fatalf("epic = %s, want in_progress (one child reopened, one still closed)", got)
	}
}

// decompose drives Decompose through RecordEvent.
func decompose(t *testing.T, s *Store, id string, titles []string) ([]Task, error) {
	t.Helper()
	var kids []Task
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.decomposed", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			kids, err = Decompose(tx, taskTestNow, id, titles, "stig", eventID)
			return err
		})
	return kids, err
}

func TestDecompose(t *testing.T) {
	s := openTaskStore(t)
	in := defaultTaskInput()
	in.Kind = "bug"
	in.Priority = "high"
	in.Concern = "security"
	parent := createTask(t, s, taskTestNow, in)

	flag := true
	if _, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.updated", nil,
		func(tx *sql.Tx, _ int64) error {
			return UpdateTaskFields(tx, taskTestNow, parent.ID, nil, nil, nil, nil, &flag)
		}); err != nil {
		t.Fatalf("set needs_decomposition: %v", err)
	}

	// A blocks edge into the parent is a reference that must survive the
	// conversion: the task on the other end of it must still resolve to the
	// same id, in place, once it becomes an epic.
	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, parent.ID, "blocks"); err != nil {
		t.Fatalf("blocks edge into parent: %v", err)
	}

	kids, err := decompose(t, s, parent.ID, []string{" Phase one ", "Phase two"})
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	if kids[0].Title != "Phase one" {
		t.Fatalf("child title = %q, want trimmed %q", kids[0].Title, "Phase one")
	}
	for _, k := range kids {
		if k.State != "draft" {
			t.Fatalf("child %s state = %s, want draft", k.ID, k.State)
		}
		if k.Priority != "high" || k.Concern != "security" || k.ProjectID != parent.ProjectID {
			t.Fatalf("child %s did not inherit project/priority/concern: %+v", k.ID, k)
		}
		if k.Kind != "bug" {
			t.Fatalf("child %s kind = %s, want the parent's pre-conversion bug", k.ID, k.Kind)
		}
	}

	got, err := s.GetTask(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Kind != "epic" {
		t.Fatalf("parent kind = %s, want epic", got.Kind)
	}
	if got.NeedsDecomposition {
		t.Fatal("parent still flagged needs_decomposition")
	}
	if got.ID != parent.ID {
		t.Fatalf("parent id changed to %s, want %s", got.ID, parent.ID)
	}

	// The reference-survival check: the pre-existing blocks edge into the
	// parent must still resolve to the same task, now converted in place.
	_, inEdges, err := s.ListEdges(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	wantBlock := Edge{FromTask: blocker.ID, ToTask: parent.ID, Type: "blocks"}
	if !slices.Contains(inEdges, wantBlock) {
		t.Fatalf("blocks edge into %s did not survive decomposition: in=%v", parent.ID, inEdges)
	}

	children, err := s.ListTasks(t.Context(), TaskFilter{Parent: parent.ID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("wired children = %d, want 2", len(children))
	}
}

func TestDecomposeRejectsLeasedTask(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := s.Claim(t.Context(), parent.ID, "stig", "wt-1", time.Hour); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, err := decompose(t, s, parent.ID, []string{"A"})
	if !errors.Is(err, ErrLeased) {
		t.Fatalf("error = %v, want ErrLeased", err)
	}
}

func TestDecomposeRejectsEmptyAndBlankTitles(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := decompose(t, s, parent.ID, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty error = %v, want ErrInvalidInput", err)
	}
	if _, err := decompose(t, s, parent.ID, []string{"A", "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank-title error = %v, want ErrInvalidInput", err)
	}
}

// TestDecomposeRespectsDepthCap: a task that is already a child cannot be
// decomposed further without exceeding the two-edge cap. The error message is
// pinned (rather than just the error class) so this fails if Decompose's own
// depth guard is deleted and the rejection instead comes from AddEdge's
// checkHierarchy, which would produce a different ErrInvalidInput message.
func TestDecomposeRespectsDepthCap(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("mid under epic: %v", err)
	}
	if _, err := decompose(t, s, mid.ID, []string{"A"}); err != nil {
		t.Fatalf("decompose at depth 1 should be allowed: %v", err)
	}

	deeper, err := s.ListTasks(t.Context(), TaskFilter{Parent: mid.ID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	_, err = decompose(t, s, deeper[0].ID, []string{"B"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "deepest allowed level") {
		t.Fatalf("decompose at depth 2 error = %v, want ErrInvalidInput naming the depth cap", err)
	}
}

// TestDecomposeRejectsEpic: decompose is for splitting an oversized task, not
// for re-splitting a container. An already-epic parent must be rejected
// rather than guessing a child kind for it.
func TestDecomposeRejectsEpic(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, err := decompose(t, s, epic.ID, []string{"A"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// TestDecomposeRejectsDeliveredTask pins the doc comment's claim that
// Decompose rejects from the delivery states an epic can never occupy.
func TestDecomposeRejectsDeliveredTask(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, parent.ID, "in_review")
	_, err := decompose(t, s, parent.ID, []string{"A"})
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("error = %v, want ErrBadTransition", err)
	}
}

// TestDecomposeRollsMergedParentToReady pins the trailing ResolveHierarchy
// call: a merged parent's fresh, all-draft children roll it back to ready
// rather than leaving it stuck in a state its new children contradict.
func TestDecomposeRollsMergedParentToReady(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, parent.ID, "merged")

	if _, err := decompose(t, s, parent.ID, []string{"A"}); err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if got := stateOf(t, s, parent.ID); got != "ready" {
		t.Fatalf("epic = %s, want ready (fresh draft children roll a merged parent back)", got)
	}
}

// TestDecomposeUnknownParent pins ErrNotFound for a parent id that doesn't
// exist.
func TestDecomposeUnknownParent(t *testing.T) {
	s := openTaskStore(t)
	_, err := decompose(t, s, "HDB-999", []string{"A"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
