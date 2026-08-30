package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestChildOfCycleRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	// t1, t2, and t3 each stand in as a child_of parent below; since 029 §2
	// any ordinary task may be one.
	t1 := createTask(t, s, taskTestNow, containerInput())
	t2 := createTask(t, s, taskTestNow, containerInput())
	t3 := createTask(t, s, taskTestNow, containerInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge %s child_of %s: %v", t1.ID, t2.ID, err)
	}
	err := addEdge(t, s, t2.ID, t1.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("direct cycle: want ErrCycle, got %v", err)
	}

	// Transitive cycle: t2 child_of t3 makes the chain t1 -> t2 -> t3;
	// t3 child_of t1 would close the loop.
	if err := addEdge(t, s, t2.ID, t3.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge %s child_of %s: %v", t2.ID, t3.ID, err)
	}
	err = addEdge(t, s, t3.ID, t1.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("transitive cycle: want ErrCycle, got %v", err)
	}
}

func TestAddEdgeDuplicateRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge: want ErrEdgeExists, got %v", err)
	}
}

func TestAddEdgeSelfRejected(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, typ := range []string{"child_of", "blocks"} {
		if err := addEdge(t, s, task.ID, task.ID, typ); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("self-edge %s: want ErrInvalidInput, got %v", typ, err)
		}
	}
}

func TestAddEdgeUnknownTask(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, task.ID, "HDB-999", "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge to unknown task: want ErrNotFound, got %v", err)
	}
	if err := addEdge(t, s, "HDB-999", task.ID, "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge from unknown task: want ErrNotFound, got %v", err)
	}
}

func TestRemoveEdgeAndListEdges(t *testing.T) {
	t.Parallel()
	s := openTaskStore(t)
	ctx := t.Context()

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	out, in, err := s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t1.ID, err)
	}
	wantOut := []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "blocks"}}
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

	if err := removeEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}
	out, in, err = s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges after remove: %v", err)
	}
	if len(out) != 0 || len(in) != 0 {
		t.Fatalf("ListEdges after remove: out=%v in=%v, want both empty", out, in)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrNotFound) {
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

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge t1->t2 blocks: %v", err)
	}
	if err := addEdge(t, s, t3.ID, t1.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge t3->t1 child_of: %v", err)
	}
	if err := addEdge(t, s, t4.ID, t5.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge t4->t5 blocks: %v", err)
	}

	// t5 is deliberately left out of the requested set.
	m, err := s.ListEdgesForTasks(ctx, []string{t1.ID, t2.ID, t3.ID, t4.ID})
	if err != nil {
		t.Fatalf("ListEdgesForTasks: %v", err)
	}

	want1 := TaskEdges{
		Out: []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "blocks"}},
		In:  []Edge{{FromTask: t3.ID, ToTask: t1.ID, Type: "child_of"}},
	}
	if got := m[t1.ID]; !reflect.DeepEqual(got, want1) {
		t.Fatalf("m[t1] = %+v, want %+v", got, want1)
	}

	want2 := TaskEdges{
		In: []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "blocks"}},
	}
	if got := m[t2.ID]; !reflect.DeepEqual(got, want2) {
		t.Fatalf("m[t2] = %+v, want %+v", got, want2)
	}

	want4 := TaskEdges{
		Out: []Edge{{FromTask: t4.ID, ToTask: t5.ID, Type: "blocks"}},
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
