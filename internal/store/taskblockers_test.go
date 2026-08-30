package store

import (
	"errors"
	"testing"
)

// TestBlockerTree walks a chain, a diamond and a cycle in one graph:
//
//	root ← a ← c        (chain, c at depth 2)
//	root ← b ← c        (diamond: c blocks both a and b)
//	          c ← d ← c (cycle, stopped at the repeat)
//
// plus a merged blocker, which is closed and so blocks nothing.
func TestBlockerTree(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	root := createTask(t, s, leaseTestNow, defaultTaskInput())
	a := createTask(t, s, leaseTestNow, defaultTaskInput())
	b := createTask(t, s, leaseTestNow, defaultTaskInput())
	c := createTask(t, s, leaseTestNow, defaultTaskInput())
	d := createTask(t, s, leaseTestNow, defaultTaskInput())
	closed := createTask(t, s, leaseTestNow, defaultTaskInput())
	walkTo(t, s, closed.ID, "merged")

	for _, e := range [][2]string{
		{a.ID, root.ID}, {b.ID, root.ID}, {closed.ID, root.ID},
		{c.ID, a.ID}, {c.ID, b.ID},
		{d.ID, c.ID}, {c.ID, d.ID},
	} {
		if err := addEdge(t, s, e[0], e[1], "blocks"); err != nil {
			t.Fatalf("add blocks edge %s -> %s: %v", e[0], e[1], err)
		}
	}

	tree, err := s.BlockerTree(ctx, root.ID)
	if err != nil {
		t.Fatalf("BlockerTree: %v", err)
	}
	if tree.Root != root.ID {
		t.Fatalf("root = %q, want %q", tree.Root, root.ID)
	}

	type edge struct{ blocker, via string }
	got := map[edge]int{}
	cycles := map[edge]bool{}
	for _, n := range tree.Blockers {
		e := edge{n.ID, n.Via}
		if _, dup := got[e]; dup {
			t.Fatalf("edge %s->%s reported twice: %+v", n.ID, n.Via, tree.Blockers)
		}
		got[e] = n.Depth
		cycles[e] = n.Cycle
		if n.Title == "" || n.State == "" || n.Project == "" {
			t.Fatalf("node %+v: title/state/project must be populated", n)
		}
	}

	want := map[edge]int{
		{a.ID, root.ID}: 1,
		{b.ID, root.ID}: 1,
		{c.ID, a.ID}:    2,
		{c.ID, b.ID}:    2,
		{d.ID, c.ID}:    3,
		{c.ID, d.ID}:    4, // the cycle's repeat, where the walk stops
	}
	for e, depth := range want {
		if got[e] != depth {
			t.Fatalf("edge %s->%s depth = %d, want %d (all: %+v)", e.blocker, e.via, got[e], depth, tree.Blockers)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("blockers = %+v, want exactly %d edges — a closed blocker must not appear", tree.Blockers, len(want))
	}
	if !cycles[edge{c.ID, d.ID}] {
		t.Fatalf("%s->%s must be marked as the cycle repeat: %+v", c.ID, d.ID, tree.Blockers)
	}

	// An unblocked task reports empty slices, never null, so a --json
	// consumer never has to distinguish the two.
	leaf, err := s.BlockerTree(ctx, closed.ID)
	if err != nil {
		t.Fatalf("BlockerTree of unblocked task: %v", err)
	}
	if leaf.Blockers == nil || len(leaf.Blockers) != 0 || leaf.BlockingPlans == nil {
		t.Fatalf("unblocked tree = %+v, want empty non-nil slices", leaf)
	}

	// An unknown root is ErrNotFound, so the handler answers 404 rather than
	// an empty tree that reads as "nothing blocks it".
	if _, err := s.BlockerTree(ctx, "WL-999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("BlockerTree of unknown task = %v, want ErrNotFound", err)
	}
}

// TestBlockerForest checks the root selection the id-less form depends on:
// a chain roots only at its top, an independent blocked task gets its own
// tree, and a task that blocks something but is not itself blocked is never
// a root.
func TestBlockerForest(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	top := createTask(t, s, leaseTestNow, defaultTaskInput())
	mid := createTask(t, s, leaseTestNow, defaultTaskInput())
	bottom := createTask(t, s, leaseTestNow, defaultTaskInput())
	other := createTask(t, s, leaseTestNow, defaultTaskInput())
	otherBlocker := createTask(t, s, leaseTestNow, defaultTaskInput())
	_ = createTask(t, s, leaseTestNow, defaultTaskInput()) // unblocked, never a root

	for _, e := range [][2]string{
		{mid.ID, top.ID}, {bottom.ID, mid.ID}, {otherBlocker.ID, other.ID},
	} {
		if err := addEdge(t, s, e[0], e[1], "blocks"); err != nil {
			t.Fatalf("add blocks edge %s -> %s: %v", e[0], e[1], err)
		}
	}

	trees, err := s.BlockerForest(ctx, "horndb")
	if err != nil {
		t.Fatalf("BlockerForest: %v", err)
	}
	roots := make([]string, 0, len(trees))
	for _, tr := range trees {
		roots = append(roots, tr.Root)
	}
	// mid is blocked, but bottom's tree already shows it under top, so it is
	// not a root of its own. bottom, otherBlocker and unblocked are not
	// blocked at all; the sixth task is blocked by nothing.
	if len(roots) != 2 || roots[0] == roots[1] {
		t.Fatalf("roots = %v, want exactly {%s, %s}", roots, top.ID, other.ID)
	}
	for _, want := range []string{top.ID, other.ID} {
		found := false
		for _, r := range roots {
			found = found || r == want
		}
		if !found {
			t.Fatalf("roots = %v, missing %s", roots, want)
		}
	}
	for _, tr := range trees {
		if tr.Root != top.ID {
			continue
		}
		if len(tr.Blockers) != 2 {
			t.Fatalf("%s tree = %+v, want mid and bottom", top.ID, tr.Blockers)
		}
		if tr.Blockers[0].ID != mid.ID || tr.Blockers[1].ID != bottom.ID ||
			tr.Blockers[1].Via != mid.ID || tr.Blockers[1].Depth != 2 {
			t.Fatalf("%s chain = %+v, want %s at depth 1 then %s via %s at depth 2",
				top.ID, tr.Blockers, mid.ID, bottom.ID, mid.ID)
		}
	}

	// A project with nothing blocked returns an empty, non-nil slice.
	empty, err := s.BlockerForest(ctx, "no-such-project")
	if err != nil {
		t.Fatalf("BlockerForest of empty scope: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty forest = %+v, want empty non-nil slice", empty)
	}
}
