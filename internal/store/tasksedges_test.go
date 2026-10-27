package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestChildOfCycleRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	// t1, t2, and t3 each stand in as a childOf parent below; since WL-REQ-120
	// any ordinary task may be one.
	t1 := createTask(t, s, taskTestNow, containerInput())
	t2 := createTask(t, s, taskTestNow, containerInput())
	t3 := createTask(t, s, taskTestNow, containerInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "childOf"); err != nil {
		t.Fatalf("AddEdge %s childOf %s: %v", t1.ID, t2.ID, err)
	}
	err := addEdge(t, s, t2.ID, t1.ID, "childOf")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("direct cycle: want ErrCycle, got %v", err)
	}

	// Transitive cycle: t2 childOf t3 makes the chain t1 -> t2 -> t3;
	// t3 childOf t1 would close the loop.
	if err := addEdge(t, s, t2.ID, t3.ID, "childOf"); err != nil {
		t.Fatalf("AddEdge %s childOf %s: %v", t2.ID, t3.ID, err)
	}
	err = addEdge(t, s, t3.ID, t1.ID, "childOf")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("transitive cycle: want ErrCycle, got %v", err)
	}
}

func TestAddEdgeDuplicateRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t2.ID, t1.ID, "dependsOn"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := addEdge(t, s, t2.ID, t1.ID, "dependsOn"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge: want ErrEdgeExists, got %v", err)
	}
}

func TestAddEdgeSelfRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, typ := range []string{"childOf", "dependsOn"} {
		if err := addEdge(t, s, task.ID, task.ID, typ); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("self-edge %s: want ErrInvalidInput, got %v", typ, err)
		}
	}
}

func TestAddEdgeUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, "HDB-999", task.ID, "dependsOn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge to unknown task: want ErrNotFound, got %v", err)
	}
	if err := addEdge(t, s, task.ID, "HDB-999", "dependsOn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge from unknown task: want ErrNotFound, got %v", err)
	}
}

func TestRemoveEdgeAndListEdges(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "dependsOn"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	out, in, err := s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t1.ID, err)
	}
	wantOut := []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "dependsOn"}}
	if !reflect.DeepEqual(out, wantOut) || len(in) != 0 {
		t.Fatalf("ListEdges %s: out=%v in=%v, want out=%v in=[]", t1.ID, out, in, wantOut)
	}
	out, in, err = s.ListEdges(ctx, t2.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t2.ID, err)
	}
	if len(out) != 0 || !reflect.DeepEqual(in, wantOut) {
		t.Fatalf("ListEdges %s: out=%v in=%v, want out=[] in=%v", t2.ID, out, in, wantOut)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "dependsOn"); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}
	out, in, err = s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges after remove: %v", err)
	}
	if len(out) != 0 || len(in) != 0 {
		t.Fatalf("ListEdges after remove: out=%v in=%v, want both empty", out, in)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "dependsOn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveEdge absent: want ErrNotFound, got %v", err)
	}
}

func TestListEdgesForTasks(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())
	t3 := createTask(t, s, taskTestNow, defaultTaskInput())
	t4 := createTask(t, s, taskTestNow, defaultTaskInput())
	t5 := createTask(t, s, taskTestNow, defaultTaskInput())
	tNoEdges := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "dependsOn"); err != nil {
		t.Fatalf("AddEdge t1->t2 dependsOn: %v", err)
	}
	if err := addEdge(t, s, t3.ID, t1.ID, "childOf"); err != nil {
		t.Fatalf("AddEdge t3->t1 childOf: %v", err)
	}
	if err := addEdge(t, s, t4.ID, t5.ID, "dependsOn"); err != nil {
		t.Fatalf("AddEdge t4->t5 dependsOn: %v", err)
	}

	// t5 is deliberately left out of the requested set.
	m, err := s.ListEdgesForTasks(ctx, []string{t1.ID, t2.ID, t3.ID, t4.ID})
	if err != nil {
		t.Fatalf("ListEdgesForTasks: %v", err)
	}

	want1 := TaskEdges{
		Out: []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "dependsOn"}},
		In:  []Edge{{FromTask: t3.ID, ToTask: t1.ID, Type: "childOf"}},
	}
	if got := m[t1.ID]; !reflect.DeepEqual(got, want1) {
		t.Fatalf("m[t1] = %+v, want %+v", got, want1)
	}

	want2 := TaskEdges{
		In: []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "dependsOn"}},
	}
	if got := m[t2.ID]; !reflect.DeepEqual(got, want2) {
		t.Fatalf("m[t2] = %+v, want %+v", got, want2)
	}

	want4 := TaskEdges{
		Out: []Edge{{FromTask: t4.ID, ToTask: t5.ID, Type: "dependsOn"}},
	}
	if got := m[t4.ID]; !reflect.DeepEqual(got, want4) {
		t.Fatalf("m[t4] = %+v, want %+v", got, want4)
	}

	if _, ok := m[t5.ID]; ok {
		t.Fatalf("m[t5] present, want absent (t5 outside requested set)")
	}
	if _, ok := m[tNoEdges.ID]; ok {
		t.Fatalf("m[tNoEdges] present, want absent (no edges at all)")
	}

	wantOut, wantIn, err := s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t1.ID, err)
	}
	single, err := s.ListEdgesForTasks(ctx, []string{t1.ID})
	if err != nil {
		t.Fatalf("ListEdgesForTasks single: %v", err)
	}
	got := single[t1.ID]
	if !reflect.DeepEqual(got.Out, wantOut) || !reflect.DeepEqual(got.In, wantIn) {
		t.Fatalf("ListEdgesForTasks disagrees with ListEdges for %s: got out=%v in=%v, want out=%v in=%v",
			t1.ID, got.Out, got.In, wantOut, wantIn)
	}
}

func TestListEdgesForTasksEmpty(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	m, err := s.ListEdgesForTasks(ctx, nil)
	if err != nil {
		t.Fatalf("ListEdgesForTasks(nil): %v", err)
	}
	if m == nil {
		t.Fatalf("ListEdgesForTasks(nil): got nil map, want empty non-nil map")
	}
	if len(m) != 0 {
		t.Fatalf("ListEdgesForTasks(nil): len(m) = %d, want 0", len(m))
	}
}

// TestAddEdgeBlocksCycleRejected: a 'dependsOn' loop deadlocks every task on it,
// so the write that closes one is refused, direct or transitive.
func TestAddEdgeBlocksCycleRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())
	t3 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t2.ID, t1.ID, "dependsOn"); err != nil {
		t.Fatalf("AddEdge %s dependsOn %s: %v", t2.ID, t1.ID, err)
	}
	if err := addEdge(t, s, t1.ID, t2.ID, "dependsOn"); !errors.Is(err, ErrCycle) {
		t.Fatalf("direct cycle: want ErrCycle, got %v", err)
	}
	if err := addEdge(t, s, t3.ID, t2.ID, "dependsOn"); err != nil {
		t.Fatalf("AddEdge %s dependsOn %s: %v", t3.ID, t2.ID, err)
	}
	if err := addEdge(t, s, t1.ID, t3.ID, "dependsOn"); !errors.Is(err, ErrCycle) {
		t.Fatalf("transitive cycle: want ErrCycle, got %v", err)
	}
}

// TestAddEdgeRefusesBlocks: blocks is the inverse of dependsOn and is never
// stored (WL-REQ-1655).
func TestAddEdgeRefusesBlocks(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AddEdge blocks: want ErrInvalidInput, got %v", err)
	}
}

// TestTaskEdgesCheckRefusesBlocks: the CHECK constraint refuses a blocks row
// written past AddEdge.
func TestTaskEdgesCheckRefusesBlocks(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())
	_, err := s.db.Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'blocks', now())`,
		t1.ID, t2.ID)
	if !isCheckViolationOn(err, "task_edges_type_check") {
		t.Fatalf("raw blocks insert: want task_edges_type_check violation, got %v", err)
	}
}

// TestDependsOnMigrationRoundTrip: the migration rewrites "A blocks B" as
// "B dependsOn A", and its down migration restores the blocks row.
func TestDependsOnMigrationRoundTrip(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	db := s.DBForTests()
	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())

	edge := func(from, to, typ string) bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM task_edges WHERE from_task = $1 AND to_task = $2 AND type = $3`,
			from, to, typ).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	migrateTo(t, s, 119)
	insertEdgeRaw(t, s, a.ID, b.ID, "blocks")
	migrateTo(t, s, 120)
	if !edge(b.ID, a.ID, "dependsOn") || edge(a.ID, b.ID, "blocks") {
		t.Fatalf("after up: want only %s dependsOn %s", b.ID, a.ID)
	}
	migrateTo(t, s, 119)
	if !edge(a.ID, b.ID, "blocks") || edge(b.ID, a.ID, "dependsOn") {
		t.Fatalf("after down: want only %s blocks %s", a.ID, b.ID)
	}
	migrateTo(t, s, 0)
}
