package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parentA := createTask(t, s, taskTestNow, defaultTaskInput())
	parentB := createTask(t, s, taskTestNow, defaultTaskInput())

	older, newer := taskTestNow, taskTestNow.Add(time.Hour)
	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, parentA.ID, older); err != nil {
		t.Fatalf("insert first parent edge: %v", err)
	}
	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, parentB.ID, newer); err != nil {
		t.Fatalf("insert second parent edge: %v", err)
	}

	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}

	rows, err := s.DBForTests().Query(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`, child.ID)
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
	if len(survivors) != 1 || survivors[0] != parentB.ID {
		t.Fatalf("surviving child_of edges = %v, want [%s] (the later one)", survivors, parentB.ID)
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

	got, err := s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{Closed: 0, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}

	walkTo(t, s, kids[0].ID, "merged")
	if err := transition(t, s, taskTestNow, kids[1].ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
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
		t.Fatalf("children = %v, want [%s]", ids(kids), child.ID)
	}

	epics, err := s.ListTasks(t.Context(), TaskFilter{Kind: "epic"})
	if err != nil {
		t.Fatalf("ListTasks kind: %v", err)
	}
	if len(epics) != 1 || epics[0].ID != epic.ID {
		t.Fatalf("epics = %v, want [%s]", ids(epics), epic.ID)
	}
	_ = loose
}

func ids(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}
