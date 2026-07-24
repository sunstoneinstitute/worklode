package store

import (
	"reflect"
	"testing"
)

func TestBlockingFanOutChainAndBranch(t *testing.T) {
	s := openTaskStore(t)

	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())
	c := createTask(t, s, taskTestNow, defaultTaskInput())
	d := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks b: %v", err)
	}
	if err := addEdge(t, s, b.ID, c.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge b blocks c: %v", err)
	}
	if err := addEdge(t, s, a.ID, d.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks d: %v", err)
	}

	got, err := s.BlockingFanOut(t.Context())
	if err != nil {
		t.Fatalf("BlockingFanOut: %v", err)
	}
	want := map[string]int{a.ID: 3, b.ID: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockingFanOut: got %v, want %v", got, want)
	}
	if _, ok := got[c.ID]; ok {
		t.Fatalf("BlockingFanOut: %s should be absent (fan-out 0), got %v", c.ID, got)
	}
	if _, ok := got[d.ID]; ok {
		t.Fatalf("BlockingFanOut: %s should be absent (fan-out 0), got %v", d.ID, got)
	}
}

func TestBlockingFanOutDiamond(t *testing.T) {
	s := openTaskStore(t)

	a := createTask(t, s, taskTestNow, defaultTaskInput())
	b := createTask(t, s, taskTestNow, defaultTaskInput())
	c := createTask(t, s, taskTestNow, defaultTaskInput())
	d := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, a.ID, b.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks b: %v", err)
	}
	if err := addEdge(t, s, a.ID, c.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge a blocks c: %v", err)
	}
	if err := addEdge(t, s, b.ID, d.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge b blocks d: %v", err)
	}
	if err := addEdge(t, s, c.ID, d.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge c blocks d: %v", err)
	}

	got, err := s.BlockingFanOut(t.Context())
	if err != nil {
		t.Fatalf("BlockingFanOut: %v", err)
	}
	// d is reachable from a via both b and c, but must be counted once.
	want := map[string]int{a.ID: 3, b.ID: 1, c.ID: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlockingFanOut diamond: got %v, want %v", got, want)
	}
	if _, ok := got[d.ID]; ok {
		t.Fatalf("BlockingFanOut diamond: %s should be absent (fan-out 0), got %v", d.ID, got)
	}
}
