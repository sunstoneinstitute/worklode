package store

import (
	"errors"
	"testing"
)

func TestBrief(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	// Claim before adding blockers: Claim refuses a blocked task, so the
	// lease has to be acquired first, then the blocking edge added.
	lease, err := s.Claim(ctx, task.ID, "stig", "host:/wt-1", 0)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	openBlocker := createTask(t, s, leaseTestNow, defaultTaskInput()) // stays ready = open
	if err := addEdge(t, s, openBlocker.ID, task.ID, "blocks"); err != nil {
		t.Fatalf("add open blocks edge: %v", err)
	}
	// Blockers in a closed state (see closedStates) no longer block, so they
	// must not appear in the brief -- merged and everything past it.
	for _, state := range []string{"merged", "deployed_dev", "deployed_prod", "released"} {
		closedBlocker := createTask(t, s, leaseTestNow, defaultTaskInput())
		walkTo(t, s, closedBlocker.ID, state)
		if err := addEdge(t, s, closedBlocker.ID, task.ID, "blocks"); err != nil {
			t.Fatalf("add %s blocks edge: %v", state, err)
		}
	}

	b, err := s.Brief(ctx, task.ID)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if b.Task.ID != task.ID || b.Body != task.Body {
		t.Fatalf("brief task = %+v, want id=%s body=%q", b.Task, task.ID, task.Body)
	}
	if want := "lode/" + task.ID + "-a-task"; b.Branch != want {
		t.Fatalf("branch = %q, want %q", b.Branch, want)
	}
	if len(b.OpenBlockers) != 1 || b.OpenBlockers[0].ID != openBlocker.ID {
		t.Fatalf("open blockers = %+v, want just %s", b.OpenBlockers, openBlocker.ID)
	}
	if b.OpenBlockers[0].State != "ready" || b.OpenBlockers[0].Title == "" {
		t.Fatalf("open blocker projection = %+v, want state/title populated", b.OpenBlockers[0])
	}
	if b.Lease == nil || b.Lease.ID != lease.ID || b.Lease.Worktree != "host:/wt-1" {
		t.Fatalf("lease = %+v, want active lease on host:/wt-1", b.Lease)
	}
	if b.GoverningDesign != nil || b.AffectedComponents != nil || b.DefinitionOfDone != nil {
		t.Fatalf("reserved fields must stay nil in v1: %+v", b)
	}
}

func TestBriefNoBlockersNoLease(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	b, err := s.Brief(ctx, task.ID)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.OpenBlockers) != 0 {
		t.Fatalf("open blockers = %+v, want none", b.OpenBlockers)
	}
	if b.Lease != nil {
		t.Fatalf("lease = %+v, want nil", b.Lease)
	}
}

// TestBriefOpenBlockersMultiCharKey regression-tests openBlockers' ordering
// query against a project whose key is not 2 chars. The bug: openBlockers
// ordered by CAST(substr(id, 4) AS INTEGER), which assumes a 3-char "WL-"
// prefix. For a 4-char key like "DEMO", substr("DEMO-2", 4) = "O-2", which
// is not valid integer input, so the query used to error (surfacing as a 500
// on the task-brief read path) instead of ordering blockers numerically.
func TestBriefOpenBlockersMultiCharKey(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "demo", "Demo", "DEMO"); err != nil {
		t.Fatalf("CreateProject demo: %v", err)
	}

	demoInput := defaultTaskInput()
	demoInput.ProjectID = "demo"

	target := createTask(t, s, leaseTestNow, demoInput)   // DEMO-1
	blocker1 := createTask(t, s, leaseTestNow, demoInput) // DEMO-2
	blocker2 := createTask(t, s, leaseTestNow, demoInput) // DEMO-3

	if err := addEdge(t, s, blocker1.ID, target.ID, "blocks"); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker1.ID, target.ID, err)
	}
	if err := addEdge(t, s, blocker2.ID, target.ID, "blocks"); err != nil {
		t.Fatalf("add blocks edge %s -> %s: %v", blocker2.ID, target.ID, err)
	}

	b, err := s.Brief(ctx, target.ID)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.OpenBlockers) != 2 {
		t.Fatalf("open blockers = %+v, want 2", b.OpenBlockers)
	}
	if b.OpenBlockers[0].ID != "DEMO-2" || b.OpenBlockers[1].ID != "DEMO-3" {
		t.Fatalf("open blockers = [%s %s], want [DEMO-2 DEMO-3]",
			b.OpenBlockers[0].ID, b.OpenBlockers[1].ID)
	}
}

func TestBriefNotFound(t *testing.T) {
	s, _ := openLeaseStore(t)
	if _, err := s.Brief(t.Context(), "HDB-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Brief unknown task: err = %v, want ErrNotFound", err)
	}
}
