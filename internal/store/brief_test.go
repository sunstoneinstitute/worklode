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
	// A done blocker no longer blocks, so it must not appear in the brief.
	doneBlocker := createTask(t, s, leaseTestNow, defaultTaskInput())
	walkTo(t, s, doneBlocker.ID, "done")
	if err := addEdge(t, s, doneBlocker.ID, task.ID, "blocks"); err != nil {
		t.Fatalf("add done blocks edge: %v", err)
	}

	b, err := s.Brief(ctx, task.ID)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if b.Task.ID != task.ID || b.Body != task.Body {
		t.Fatalf("brief task = %+v, want id=%s body=%q", b.Task, task.ID, task.Body)
	}
	if want := "wl/" + task.ID + "-a-task"; b.Branch != want {
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

func TestBriefNotFound(t *testing.T) {
	s, _ := openLeaseStore(t)
	if _, err := s.Brief(t.Context(), "WL-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Brief unknown task: err = %v, want ErrNotFound", err)
	}
}
