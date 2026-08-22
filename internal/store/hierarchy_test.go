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

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestKindCheckRejectsUnknownKinds pins the tasks_kind_check constraint: it is
// exhaustive, so anything outside the six kinds of 025 §10 — including a
// plausible-sounding container kind — fails at the database.
func TestKindCheckRejectsUnknownKinds(t *testing.T) {
	s := openTaskStore(t)
	for _, kind := range []string{"container", "saga"} {
		bad := defaultTaskInput()
		bad.Kind = kind
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
			func(tx *sql.Tx, eventID int64) error {
				_, err := CreateTask(tx, taskTestNow, bad, eventID)
				return err
			})
		if !isCheckViolationOn(err, "tasks_kind_check") {
			t.Fatalf("kind %q error = %v, want a tasks_kind_check violation", kind, err)
		}
	}
}

// TestSingleParentIndex checks that the partial unique index rejects a second
// child_of edge out of one task, whichever parent it points at.
func TestSingleParentIndex(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parentA := createTask(t, s, taskTestNow, containerInput())
	parentB := createTask(t, s, taskTestNow, containerInput())

	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, parentA.ID, taskTestNow); err != nil {
		t.Fatalf("first parent edge: %v", err)
	}
	_, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, parentB.ID, taskTestNow)
	if err == nil {
		t.Fatal("second parent edge succeeded, want a unique violation")
	}
	if !isUniqueViolationOn(err, "task_edges_single_parent") {
		t.Fatalf("error = %v, want a task_edges_single_parent unique violation", err)
	}
}

// TestAddEdgeFollowUpTo checks the third edge type: it is accepted, it is not
// project-scoped the way child_of is, and it confers no parent-hood — the
// origin gains no children and no roll-up.
func TestAddEdgeFollowUpTo(t *testing.T) {
	s := openTaskStore(t)
	origin := createTask(t, s, taskTestNow, defaultTaskInput())
	followUp := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, taskTestNow, followUp.ID, origin.ID, "follow_up_to", eventID)
		}); err != nil {
		t.Fatalf("AddEdge follow_up_to: %v", err)
	}

	progress, err := s.ChildProgress(t.Context(), origin.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if progress.Total != 0 {
		t.Fatalf("origin progress = %+v, want zero total: a follow-up is not a child", progress)
	}
	parent, err := s.ParentOf(t.Context(), followUp.ID)
	if err != nil {
		t.Fatalf("ParentOf: %v", err)
	}
	if parent != nil {
		t.Fatalf("follow-up parent = %+v, want nil", parent)
	}
}

// TestAddEdgeMissingEndpoint pins the error precedence of the endpoint
// existence check now that both endpoints are read in one query: a missing
// endpoint is ErrNotFound naming that endpoint, and when both are missing it
// is the from end that is named.
func TestAddEdgeMissingEndpoint(t *testing.T) {
	s := openTaskStore(t)
	real := createTask(t, s, taskTestNow, defaultTaskInput())

	for _, tc := range []struct {
		name           string
		from, to, want string
	}{
		{"missing from", "WL-9001", real.ID, "WL-9001"},
		{"missing to", real.ID, "WL-9002", "WL-9002"},
		{"both missing", "WL-9001", "WL-9002", "WL-9001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
				func(tx *sql.Tx, eventID int64) error {
					return AddEdge(tx, taskTestNow, tc.from, tc.to, "blocks", eventID)
				})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("AddEdge error = %v, want ErrNotFound", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("AddEdge error = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// TestSingleOriginIndex pins the partial unique index: a task has at most one
// origin, whichever task the second edge points at.
func TestSingleOriginIndex(t *testing.T) {
	s := openTaskStore(t)
	followUp := createTask(t, s, taskTestNow, defaultTaskInput())
	originA := createTask(t, s, taskTestNow, defaultTaskInput())
	originB := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, taskTestNow, followUp.ID, originA.ID, "follow_up_to", eventID)
		}); err != nil {
		t.Fatalf("first origin: %v", err)
	}
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, taskTestNow, followUp.ID, originB.ID, "follow_up_to", eventID)
		})
	if !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("second origin error = %v, want ErrEdgeExists", err)
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

// containerInput is the shared fixture for a task that will take children.
// Since 029 §2 there is no kind to declare — an ordinary task becomes a
// container by acquiring child_of edges — so this differs from
// defaultTaskInput only in its title.
func containerInput() TaskInput {
	in := defaultTaskInput()
	in.Title = "a container"
	return in
}

// TestAddEdgeAcceptsOrdinaryParent pins 029 §2's change to checkHierarchy:
// any ordinary task may be a parent, and the edge is what makes it one.
func TestAddEdgeAcceptsOrdinaryParent(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parent := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("ordinary task as parent: %v", err)
	}
	got, err := s.GetTask(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Kind != defaultTaskInput().Kind {
		t.Fatalf("parent kind = %q, want it untouched at %q", got.Kind, defaultTaskInput().Kind)
	}
}

func TestAddEdgeRejectsSecondParent(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parentA := createTask(t, s, taskTestNow, containerInput())
	parentB := createTask(t, s, taskTestNow, containerInput())

	if err := addEdge(t, s, child.ID, parentA.ID, "child_of"); err != nil {
		t.Fatalf("first parent: %v", err)
	}
	err := addEdge(t, s, child.ID, parentB.ID, "child_of")
	if !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("error = %v, want ErrEdgeExists", err)
	}
	// The baseline duplicate-edge rule still applies to the same pair.
	if err := addEdge(t, s, child.ID, parentA.ID, "child_of"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge error = %v, want ErrEdgeExists", err)
	}
}

func TestAddEdgeRejectsCrossProject(t *testing.T) {
	s := openTaskStore(t)
	if err := s.CreateProject(t.Context(), "other", "Other", "OTH"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	container := createTask(t, s, taskTestNow, containerInput())
	otherIn := defaultTaskInput()
	otherIn.ProjectID = "other"
	child := createTask(t, s, taskTestNow, otherIn)

	err := addEdge(t, s, child.ID, container.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestAddEdgeEnforcesDepthCap(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	mid := createTask(t, s, taskTestNow, containerInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	deep := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, mid.ID, container.ID, "child_of"); err != nil {
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
	top := createTask(t, s, taskTestNow, containerInput())
	mid := createTask(t, s, taskTestNow, containerInput())
	sub := createTask(t, s, taskTestNow, containerInput())
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
	container := createTask(t, s, taskTestNow, containerInput())
	child := createTask(t, s, taskTestNow, containerInput())

	if err := addEdge(t, s, child.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child under container: %v", err)
	}
	err := addEdge(t, s, container.ID, child.ID, "child_of")
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
	parent := createTask(t, s, taskTestNow, containerInput())
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
	top := createTask(t, s, taskTestNow, containerInput())
	n1 := createTask(t, s, taskTestNow, containerInput())
	n2 := createTask(t, s, taskTestNow, containerInput())
	n3 := createTask(t, s, taskTestNow, containerInput())
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
	container := createTask(t, s, taskTestNow, containerInput())
	var kids []*model.Task
	for i := 0; i < 3; i++ {
		k := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, k.ID, container.ID, "child_of"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		kids = append(kids, k)
	}
	// A blocks edge into the container shares child_of's direction (to_task =
	// container). It must not be counted: pins the e.type = 'child_of' predicate.
	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, container.ID, "blocks"); err != nil {
		t.Fatalf("blocks: %v", err)
	}

	got, err := s.ChildProgress(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (model.TaskProgress{Closed: 0, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}

	walkTo(t, s, kids[0].ID, "merged")
	walkTo(t, s, kids[1].ID, "abandoned")
	got, err = s.ChildProgress(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (model.TaskProgress{Closed: 2, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

// TestChildProgressPerRepoDoneState pins ChildProgress on the same per-repo
// predicate the blocking queries use (taskClosed, spec 004 §1.3): a merged
// child whose repo gates on released has not finished delivering, so it must
// not be counted closed until it is released.
func TestChildProgressPerRepoDoneState(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")

	container := createTask(t, s, taskTestNow, containerInput())
	kid := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, kid.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}
	landCommit(t, s, kid.ID, "acme/app", "sha-kid")

	walkTo(t, s, kid.ID, "merged")
	got, err := s.ChildProgress(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (model.TaskProgress{Closed: 0, Total: 1}); got != want {
		t.Fatalf("progress with merged child in a release-gated repo = %+v, want %+v", got, want)
	}

	if err := transition(t, s, taskTestNow, kid.ID, "merged", "released"); err != nil {
		t.Fatalf("release child: %v", err)
	}
	got, err = s.ChildProgress(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (model.TaskProgress{Closed: 1, Total: 1}); got != want {
		t.Fatalf("progress with released child = %+v, want %+v", got, want)
	}
}

func TestChildProgressNoChildren(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	got, err := s.ChildProgress(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (model.TaskProgress{}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestParentOf(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	got, err := s.ParentOf(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("ParentOf: %v", err)
	}
	if got == nil || got.ID != container.ID || got.Title != container.Title || got.State != container.State {
		t.Fatalf("parent = %+v, want id/title/state of %s", got, container.ID)
	}

	root, err := s.ParentOf(t.Context(), container.ID)
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

	container := createTask(t, s, taskTestNow, containerInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	otherParentIn := containerInput()
	otherParentIn.ProjectID = "other"
	otherParent := createTask(t, s, taskTestNow, otherParentIn)
	otherChildIn := defaultTaskInput()
	otherChildIn.ProjectID = "other"
	otherChild := createTask(t, s, taskTestNow, otherChildIn)
	if err := addEdge(t, s, otherChild.ID, otherParent.ID, "child_of"); err != nil {
		t.Fatalf("other child_of: %v", err)
	}

	scoped, err := s.ParentMap(t.Context(), "horndb")
	if err != nil {
		t.Fatalf("ParentMap horndb: %v", err)
	}
	if want := map[string]string{child.ID: container.ID}; !reflect.DeepEqual(scoped, want) {
		t.Fatalf("ParentMap(horndb) = %v, want %v", scoped, want)
	}

	all, err := s.ParentMap(t.Context(), "")
	if err != nil {
		t.Fatalf("ParentMap all: %v", err)
	}
	if want := (map[string]string{child.ID: container.ID, otherChild.ID: otherParent.ID}); !reflect.DeepEqual(all, want) {
		t.Fatalf("ParentMap(\"\") = %v, want %v", all, want)
	}
}

func TestListTasksFilterParentAndHasChildren(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	loose := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	kids, err := s.ListTasks(t.Context(), TaskFilter{Parent: container.ID})
	if err != nil {
		t.Fatalf("ListTasks parent: %v", err)
	}
	if len(kids) != 1 || kids[0].ID != child.ID {
		t.Fatalf("children = %v, want [%s]", taskIDs(kids), child.ID)
	}
	if slices.Contains(taskIDs(kids), loose.ID) {
		t.Fatalf("children = %v, must not include the parentless task %s", taskIDs(kids), loose.ID)
	}

	// HasChildren is what selects containers now that no kind declares one.
	parents, err := s.ListTasks(t.Context(), TaskFilter{HasChildren: true})
	if err != nil {
		t.Fatalf("ListTasks has_children: %v", err)
	}
	if len(parents) != 1 || parents[0].ID != container.ID {
		t.Fatalf("parents = %v, want [%s]", taskIDs(parents), container.ID)
	}
	if slices.Contains(taskIDs(parents), loose.ID) {
		t.Fatalf("parents = %v, must not include the childless task %s", taskIDs(parents), loose.ID)
	}
}

func taskIDs(tasks []model.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

// TestParentNeverInReadySet checks that a task with children stays out of the
// ranked pickup set even when it is ready, unblocked, and top-ranked by every
// other factor. Its child is draft, so only the plain task is a candidate.
func TestParentNeverInReadySet(t *testing.T) {
	s := openTaskStore(t)
	in := containerInput()
	in.Priority = "critical"
	parent := createTask(t, s, taskTestNow, in)
	kidIn := defaultTaskInput()
	kidIn.Draft = true
	kid := createTask(t, s, taskTestNow, kidIn)
	if err := addEdge(t, s, kid.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}
	plain := createTask(t, s, taskTestNow, defaultTaskInput())

	got, err := s.readyCandidates(t.Context(), "", "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	if len(got) != 1 || got[0].ID != plain.ID {
		t.Fatalf("candidates = %v, want [%s] (parent %s excluded)", taskIDs(got), plain.ID, parent.ID)
	}
}

// TestClaimRejectsParent checks the direct-claim hole: ready -> in_progress is
// a legal transition for a task with children (it is the roll-up trigger), so
// Claim needs its own guard beyond the ready-set exclusion (004 §6.1).
func TestClaimRejectsParent(t *testing.T) {
	s := openTaskStore(t)
	parent, _ := parentWithChildren(t, s, 1)
	_, err := s.Claim(t.Context(), parent.ID, "stig", "wt-1", time.Hour)
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("error = %v, want ErrBadTransition", err)
	}
}

// TestClaimAllowsChildlessTask is the other half of the inferred-container
// rule: without children the same task is an ordinary, claimable one.
func TestClaimAllowsChildlessTask(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, containerInput())
	if _, err := s.Claim(t.Context(), task.ID, "stig", "wt-1", time.Hour); err != nil {
		t.Fatalf("Claim on a childless task: %v", err)
	}
}

// TestContainerForbiddenStates checks the guard on both ends of a transition:
// a task with children can never enter a delivery state, and `lode task done`
// (in_review -> merged) reports the roll-up rule rather than a from-state
// mismatch. The guard keys off the children, not a kind (029 §2).
func TestContainerForbiddenStates(t *testing.T) {
	s := openTaskStore(t)
	parent, _ := parentWithChildren(t, s, 1)
	if got := stateOf(t, s, parent.ID); got != "ready" {
		t.Fatalf("parent = %s, want ready before the walk", got)
	}
	if err := transition(t, s, taskTestNow, parent.ID, "ready", "in_progress"); err != nil {
		t.Fatalf("ready -> in_progress: %v", err)
	}
	err := transition(t, s, taskTestNow, parent.ID, "in_progress", "in_review")
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("in_review error = %v, want ErrBadTransition", err)
	}
	if !strings.Contains(err.Error(), "has children") {
		t.Fatalf("error %q does not name the container rule", err)
	}
	err = transition(t, s, taskTestNow, parent.ID, "in_review", "merged")
	if !errors.Is(err, ErrBadTransition) || !strings.Contains(err.Error(), "has children") {
		t.Fatalf("done error = %v, want a container ErrBadTransition", err)
	}
	// The roll-up terminal is still reachable.
	if err := transition(t, s, taskTestNow, parent.ID, "in_progress", "merged"); err != nil {
		t.Fatalf("in_progress -> merged: %v", err)
	}
}

// TestChildlessTaskReachesDeliveryStates is the other half: the same states
// are legal for a task with no children, so the guard is not a blanket ban.
func TestChildlessTaskReachesDeliveryStates(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, containerInput())
	walkTo(t, s, task.ID, "in_review")
	if got := stateOf(t, s, task.ID); got != "in_review" {
		t.Fatalf("state = %s, want in_review", got)
	}
}

// TestResolveDeliveryIgnoresParents checks that a task with children carrying
// commit and deploy facts attributed to it is left alone: a container has no
// commit of its own (004 §6.4).
func TestResolveDeliveryIgnoresParents(t *testing.T) {
	s := openTaskStore(t)
	container, _ := parentWithChildren(t, s, 1)
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "delivery", nil,
		func(tx *sql.Tx, eventID int64) error {
			if err := InsertTaskCommit(tx, TaskCommit{
				TaskID: container.ID, Repo: "o/r", SHA: "abc", Source: "branch_push", SeenAt: taskTestNow,
			}); err != nil {
				return err
			}
			if _, err := AppendMainCommit(tx, "o/r", "abc", taskTestNow); err != nil {
				return err
			}
			return ResolveDelivery(tx, taskTestNow, container.ID, "o/r", eventID)
		})
	if err != nil {
		t.Fatalf("ResolveDelivery: %v", err)
	}
	got, err := s.GetTask(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state = %s, want ready (a container has no commit)", got.State)
	}
}

// closedKids builds children whose closedness follows the default merged-gated
// reading: every state from merged onward, plus abandoned, counts as closed.
func closedKids(states ...string) []childState {
	if states == nil {
		return nil
	}
	kids := make([]childState, len(states))
	for i, st := range states {
		kids[i] = childState{State: st, Closed: deliveredStateSet[st]}
	}
	return kids
}

func TestContainerTarget(t *testing.T) {
	cases := []struct {
		name     string
		children []childState
		want     string
	}{
		{"no children", nil, ""},
		{"all draft or ready", closedKids("draft", "ready"), "ready"},
		{"one started", closedKids("ready", "in_progress"), "in_progress"},
		{"one in review", closedKids("ready", "in_review"), "in_progress"},
		{"one landed, one open", closedKids("merged", "ready"), "in_progress"},
		{"all closed, one delivered", closedKids("merged", "abandoned"), "merged"},
		{"all closed via deploy", closedKids("deployed_dev", "released"), "merged"},
		{"all abandoned", closedKids("abandoned", "abandoned"), "abandoned"},
		{"all delivered", closedKids("merged", "deployed_prod"), "merged"},
		// Per-repo closedness (004 §1.3): a landed child that has not reached
		// its repo's done_state holds the parent at in_progress rather than
		// rolling it up — and must not read as un-started either, which would
		// send the parent back to ready.
		{"landed but not delivered", []childState{{State: "merged", Closed: false}}, "in_progress"},
		{"one delivered, one still delivering", []childState{
			{State: "released", Closed: true}, {State: "deployed_dev", Closed: false},
		}, "in_progress"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containerTarget(tc.children); got != tc.want {
				t.Fatalf("containerTarget(%v) = %q, want %q", tc.children, got, tc.want)
			}
		})
	}
}

// TestResolveHierarchyPerRepoDoneState pins the roll-up on the same per-repo
// predicate as ChildProgress: a parent whose only child is merged in a
// release-gated repo stays in_progress, and rolls up to merged only once that
// child is actually released. Roll-up and progress must never disagree about
// which children are done.
func TestResolveHierarchyPerRepoDoneState(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")

	container := createTask(t, s, taskTestNow, containerInput())
	kid := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, kid.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}
	landCommit(t, s, kid.ID, "acme/app", "sha-kid")

	walkTo(t, s, kid.ID, "merged")
	got, err := s.GetTask(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "in_progress" {
		t.Fatalf("parent state = %s, want in_progress (child merged, repo gates on released)", got.State)
	}

	if err := transition(t, s, taskTestNow, kid.ID, "merged", "released"); err != nil {
		t.Fatalf("release child: %v", err)
	}
	got, err = s.GetTask(t.Context(), container.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "merged" {
		t.Fatalf("parent state = %s, want merged (child released)", got.State)
	}
}

// parentWithChildren builds a task with n ready children and returns both.
func parentWithChildren(t *testing.T, s *Store, n int) (*model.Task, []*model.Task) {
	t.Helper()
	container := createTask(t, s, taskTestNow, containerInput())
	var kids []*model.Task
	for i := 0; i < n; i++ {
		k := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, k.ID, container.ID, "child_of"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		kids = append(kids, k)
	}
	return container, kids
}

func stateOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	task, err := s.GetTask(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTask %s: %v", id, err)
	}
	return task.State
}

// TestRollUpForward: the first child to start moves the container off ready, and
// the last child to close moves it to merged.
func TestRollUpForward(t *testing.T) {
	s := openTaskStore(t)
	container, kids := parentWithChildren(t, s, 2)

	if err := transition(t, s, taskTestNow, kids[0].ID, "ready", "in_progress"); err != nil {
		t.Fatalf("start child 0: %v", err)
	}
	if got := stateOf(t, s, container.ID); got != "in_progress" {
		t.Fatalf("container = %s, want in_progress", got)
	}

	// child 0 is already in in_progress and walkTo always restarts from ready,
	// so step it manually.
	if err := transition(t, s, taskTestNow, kids[0].ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("review child 0: %v", err)
	}
	if err := transition(t, s, taskTestNow, kids[0].ID, "in_review", "merged"); err != nil {
		t.Fatalf("merge child 0: %v", err)
	}
	if got := stateOf(t, s, container.ID); got != "in_progress" {
		t.Fatalf("container = %s, want in_progress with one child still open", got)
	}
	walkTo(t, s, kids[1].ID, "merged")
	if got := stateOf(t, s, container.ID); got != "merged" {
		t.Fatalf("container = %s, want merged", got)
	}
}

// TestRollUpZeroChildren: a task with no children never moves. It is an
// ordinary task and stays where it is (004 §6.5).
func TestRollUpZeroChildren(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "rollup", nil,
		func(tx *sql.Tx, eventID int64) error {
			return ResolveHierarchy(tx, taskTestNow, container.ID, eventID)
		})
	if err != nil {
		t.Fatalf("ResolveHierarchy: %v", err)
	}
	if got := stateOf(t, s, container.ID); got != "ready" {
		t.Fatalf("container = %s, want ready", got)
	}
}

func TestRollUpAllAbandoned(t *testing.T) {
	s := openTaskStore(t)
	container, kids := parentWithChildren(t, s, 2)
	for _, k := range kids {
		if err := transition(t, s, taskTestNow, k.ID, "ready", "abandoned"); err != nil {
			t.Fatalf("abandon %s: %v", k.ID, err)
		}
	}
	if got := stateOf(t, s, container.ID); got != "abandoned" {
		t.Fatalf("container = %s, want abandoned", got)
	}
}

func TestRollUpMixedAbandonedAndDelivered(t *testing.T) {
	s := openTaskStore(t)
	container, kids := parentWithChildren(t, s, 2)
	walkTo(t, s, kids[0].ID, "merged")
	if err := transition(t, s, taskTestNow, kids[1].ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if got := stateOf(t, s, container.ID); got != "merged" {
		t.Fatalf("container = %s, want merged (some of the parent's work landed)", got)
	}
}

// TestRollUpReopen: a child returning to ready puts a closed container back to
// ready. Asymmetric roll-up produces boards that lie.
func TestRollUpReopen(t *testing.T) {
	s := openTaskStore(t)
	container, kids := parentWithChildren(t, s, 1)
	walkTo(t, s, kids[0].ID, "merged")
	if got := stateOf(t, s, container.ID); got != "merged" {
		t.Fatalf("container = %s, want merged", got)
	}
	if err := transition(t, s, taskTestNow, kids[0].ID, "merged", "ready"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := stateOf(t, s, container.ID); got != "ready" {
		t.Fatalf("container = %s, want ready", got)
	}
}

// TestRollUpAttribution: the parent's state_log row carries the child's event
// id, so the timeline explains itself with no synthetic event.
func TestRollUpAttribution(t *testing.T) {
	s := openTaskStore(t)
	container, kids := parentWithChildren(t, s, 1)

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
		container.ID).Scan(&got); err != nil {
		t.Fatalf("read container state_log: %v", err)
	}
	if got != eventID {
		t.Fatalf("container state_log event_id = %d, want the child's %d", got, eventID)
	}
}

// TestRollUpDepth2Recursion: a subtask closing resolves its task, which
// resolves the container, in one transaction.
func TestRollUpDepth2Recursion(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	mid := createTask(t, s, taskTestNow, containerInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("mid under container: %v", err)
	}
	if err := addEdge(t, s, leaf.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("leaf under mid: %v", err)
	}

	walkTo(t, s, leaf.ID, "merged")
	if got := stateOf(t, s, mid.ID); got != "merged" {
		t.Fatalf("mid = %s, want merged", got)
	}
	if got := stateOf(t, s, container.ID); got != "merged" {
		t.Fatalf("container = %s, want merged", got)
	}
}

// TestRollUpReopenRoutesThroughReady: merged -> in_progress is not a legal
// edge, so a merged container whose child reopens while another stays closed
// routes through ready, the only edge out of a closed state.
func TestRollUpReopenRoutesThroughReady(t *testing.T) {
	s := openTaskStore(t)
	container, kids := parentWithChildren(t, s, 2)
	walkTo(t, s, kids[0].ID, "merged")
	walkTo(t, s, kids[1].ID, "abandoned")
	if got := stateOf(t, s, container.ID); got != "merged" {
		t.Fatalf("container = %s, want merged", got)
	}

	if err := transition(t, s, taskTestNow, kids[0].ID, "merged", "ready"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := stateOf(t, s, container.ID); got != "in_progress" {
		t.Fatalf("container = %s, want in_progress (one child reopened, one still closed)", got)
	}
}

// decompose drives Decompose through RecordEvent.
func decompose(t *testing.T, s *Store, id string, titles []string) ([]model.Task, error) {
	t.Helper()
	var kids []model.Task
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
			return UpdateTaskFields(tx, taskTestNow, parent.ID, nil, nil, nil, nil, nil, &flag, nil)
		}); err != nil {
		t.Fatalf("set needs_decomposition: %v", err)
	}

	// A blocks edge into the parent is a reference that must survive the
	// split: the task on the other end of it must still resolve to the same
	// id, in place, once the parent takes children.
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
		if k.Priority != "high" || k.Concern != "security" || k.Project != parent.Project {
			t.Fatalf("child %s did not inherit project/priority/concern: %+v", k.ID, k)
		}
		if k.Kind != "bug" {
			t.Fatalf("child %s kind = %s, want the parent's bug", k.ID, k.Kind)
		}
	}

	got, err := s.GetTask(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 029 §2 / 004 §6.10: decompose does not touch the parent's kind — the
	// child_of edges are what make it a container.
	if got.Kind != "bug" {
		t.Fatalf("parent kind = %s, want it untouched at bug", got.Kind)
	}
	if got.NeedsDecomposition {
		t.Fatal("parent still flagged needs_decomposition")
	}
	// The flag was set, so clearing it is a real change and is logged.
	var n int
	if err := s.DBForTests().QueryRow(
		`SELECT COUNT(*) FROM state_log
		  WHERE entity_id = $1 AND change->>'field' = 'needs_decomposition'`,
		parent.ID).Scan(&n); err != nil {
		t.Fatalf("count state_log: %v", err)
	}
	if n != 1 {
		t.Fatalf("needs_decomposition change rows = %d, want 1", n)
	}
	if got.ID != parent.ID {
		t.Fatalf("parent id changed to %s, want %s", got.ID, parent.ID)
	}

	// The reference-survival check: the pre-existing blocks edge into the
	// parent must still resolve to the same task, split in place.
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

// TestDecomposeLogsTheFlagOnlyWhenSet pins the provenance row: decompose is
// legal on a task that was never flagged needs_decomposition, and a change
// row claiming true -> false there would be a lie. The child_of edges are the
// record that the split happened.
func TestDecomposeLogsTheFlagOnlyWhenSet(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := decompose(t, s, parent.ID, []string{"A"}); err != nil {
		t.Fatalf("Decompose: %v", err)
	}

	var n int
	if err := s.DBForTests().QueryRow(
		`SELECT COUNT(*) FROM state_log
		  WHERE entity_id = $1 AND change->>'field' = 'needs_decomposition'`,
		parent.ID).Scan(&n); err != nil {
		t.Fatalf("count state_log: %v", err)
	}
	if n != 0 {
		t.Fatalf("needs_decomposition change rows = %d, want 0 (the flag was never set)", n)
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
	container := createTask(t, s, taskTestNow, containerInput())
	mid := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("mid under container: %v", err)
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

// TestDecomposeRejectsTaskWithChildren: decompose is for splitting an
// oversized task, not for re-splitting a container. A parent that already has
// children must be rejected — add more with AddEdge instead.
func TestDecomposeRejectsTaskWithChildren(t *testing.T) {
	s := openTaskStore(t)
	parent, _ := parentWithChildren(t, s, 1)
	_, err := decompose(t, s, parent.ID, []string{"A"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// TestDecomposeRejectsDeliveredTask pins the doc comment's claim that
// Decompose rejects from the delivery states a container can never occupy.
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
		t.Fatalf("parent = %s, want ready (fresh draft children roll a merged parent back)", got)
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

// TestTaskTree pins the whole-hierarchy read `lode task tree` makes in one
// request (WL-169): the roots are the containers with no parent of their own,
// each carries the same roll-up ChildProgress computes, and the children come
// back with them whatever their state.
func TestTaskTree(t *testing.T) {
	s := openTaskStore(t)
	top := createTask(t, s, taskTestNow, containerInput())
	done := createTask(t, s, taskTestNow, defaultTaskInput())
	open := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, k := range []string{done.ID, open.ID} {
		if err := addEdge(t, s, k, top.ID, "child_of"); err != nil {
			t.Fatalf("child_of %s: %v", k, err)
		}
	}
	// A container that is itself a child: it belongs under its own parent,
	// not as a second root.
	if err := addEdge(t, s, createTask(t, s, taskTestNow, defaultTaskInput()).ID, open.ID, "child_of"); err != nil {
		t.Fatalf("grandchild: %v", err)
	}
	loose := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, done.ID, "merged")

	nodes, err := s.TaskTree(t.Context(), TaskTreeFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	var roots []string
	for _, n := range nodes {
		roots = append(roots, n.Parent.ID)
	}
	if len(nodes) != 1 || nodes[0].Parent.ID != top.ID {
		t.Fatalf("roots = %v, want [%s] (a container with a parent is not a root, and %s has no children)",
			roots, top.ID, loose.ID)
	}
	if want := (model.TaskProgress{Closed: 1, Total: 2}); nodes[0].Progress != want {
		t.Fatalf("progress = %+v, want %+v", nodes[0].Progress, want)
	}
	if got := taskIDs(nodes[0].Children); !slices.Contains(got, done.ID) || !slices.Contains(got, open.ID) || len(got) != 2 {
		t.Fatalf("children = %v, want both %s and %s", got, done.ID, open.ID)
	}
}

// TestTaskTreeStatesNarrowContainersNotChildren pins what the state filter
// means for a tree: it selects which containers are reported, never which of
// their children are — otherwise the progress counts and the rows under a
// parent would describe different sets.
func TestTaskTreeStatesNarrowContainersNotChildren(t *testing.T) {
	s := openTaskStore(t)
	container := createTask(t, s, taskTestNow, containerInput())
	kid := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, kid.ID, container.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}
	walkTo(t, s, kid.ID, "in_progress")

	nodes, err := s.TaskTree(t.Context(), TaskTreeFilter{
		Project: "horndb", States: []string{stateOf(t, s, container.ID)},
	})
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if len(nodes) != 1 || len(nodes[0].Children) != 1 || nodes[0].Children[0].ID != kid.ID {
		t.Fatalf("nodes = %+v, want the container with its one child", nodes)
	}

	nodes, err = s.TaskTree(t.Context(), TaskTreeFilter{Project: "horndb", States: []string{"abandoned"}})
	if err != nil {
		t.Fatalf("TaskTree abandoned: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("nodes = %+v, want none: no container is abandoned", nodes)
	}
}

// TestTaskTreeRoot pins the single-container form: it reports that task and
// its children whatever the task's own parentage, and an unknown id is
// ErrNotFound rather than an empty tree.
func TestTaskTreeRoot(t *testing.T) {
	s := openTaskStore(t)
	top := createTask(t, s, taskTestNow, containerInput())
	mid := createTask(t, s, taskTestNow, containerInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, top.ID, "child_of"); err != nil {
		t.Fatalf("mid child_of top: %v", err)
	}
	if err := addEdge(t, s, leaf.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("leaf child_of mid: %v", err)
	}

	nodes, err := s.TaskTree(t.Context(), TaskTreeFilter{Project: "horndb", Root: mid.ID})
	if err != nil {
		t.Fatalf("TaskTree root: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Parent.ID != mid.ID {
		t.Fatalf("nodes = %+v, want just %s", nodes, mid.ID)
	}
	if got := taskIDs(nodes[0].Children); !slices.Equal(got, []string{leaf.ID}) {
		t.Fatalf("children = %v, want [%s]", got, leaf.ID)
	}

	if _, err := s.TaskTree(t.Context(), TaskTreeFilter{Root: "HDB-999"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// TestChildrenOfIsBulk pins the query the tree is built on: one call answers
// for every parent at once, keyed by parent, with the closedness flag
// ListTasks fills in and no row for a parent with no children.
func TestChildrenOfIsBulk(t *testing.T) {
	s := openTaskStore(t)
	a := createTask(t, s, taskTestNow, containerInput())
	b := createTask(t, s, taskTestNow, containerInput())
	childless := createTask(t, s, taskTestNow, containerInput())
	ka := createTask(t, s, taskTestNow, defaultTaskInput())
	kb := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, ka.ID, a.ID, "child_of"); err != nil {
		t.Fatalf("ka: %v", err)
	}
	if err := addEdge(t, s, kb.ID, b.ID, "child_of"); err != nil {
		t.Fatalf("kb: %v", err)
	}
	walkTo(t, s, ka.ID, "merged")

	got, err := s.ChildrenOf(t.Context(), []string{a.ID, b.ID, childless.ID})
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("children = %+v, want entries for %s and %s only", got, a.ID, b.ID)
	}
	if len(got[a.ID]) != 1 || got[a.ID][0].ID != ka.ID || !got[a.ID][0].Closed {
		t.Fatalf("children of %s = %+v, want the merged %s marked closed", a.ID, got[a.ID], ka.ID)
	}
	if len(got[b.ID]) != 1 || got[b.ID][0].ID != kb.ID || got[b.ID][0].Closed {
		t.Fatalf("children of %s = %+v, want the open %s", b.ID, got[b.ID], kb.ID)
	}
	if _, err := s.ChildrenOf(t.Context(), nil); err != nil {
		t.Fatalf("ChildrenOf(nil): %v", err)
	}
}
