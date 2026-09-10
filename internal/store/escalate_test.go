package store

import (
	"errors"
	"testing"
)

// openEscalateStore is openTaskStore plus the plan author the escalation
// assigns to: a second crew member, so the assignment is a real choice rather
// than the only actor there is.
func openEscalateStore(t *testing.T) *Store {
	t.Helper()
	s := openTaskStore(t)
	if err := s.CreateActor(t.Context(), "alice", "human", "Alice", false); err != nil {
		t.Fatalf("CreateActor alice: %v", err)
	}
	seedParticipant(t, s, "horndb", "alice", "member", false)
	return s
}

// TestEscalateTask is 025 §8.1 end to end: the lease goes, the task goes back
// to ready blocked on a design task assigned to the plan's author, and a
// second executor hitting the same gap joins that task rather than minting a
// rival.
func TestEscalateTask(t *testing.T) {
	t.Parallel()
	s := openEscalateStore(t)
	ctx := t.Context()

	plan := mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "plan", Number: 7, Slug: "007-plan",
		Body: "---\nstatus: accepted\n---\n\n# A plan\n", CreatedBy: "alice",
	})

	in := defaultTaskInput()
	in.CreatedBy = "stig"
	first := createTask(t, s, taskTestNow, in)
	second := createTask(t, s, taskTestNow, in)

	if _, err := s.Claim(ctx, first.ID, "stig", "wt-1", 0); err != nil {
		t.Fatalf("claim %s: %v", first.ID, err)
	}

	res, err := s.EscalateTask(ctx, EscalateInput{
		TaskID: first.ID, To: "plan", DocID: plan.ID, Anchor: "sec-3",
		Reason: "the plan says nothing about the empty case", ActorID: "stig",
	})
	if err != nil {
		t.Fatalf("EscalateTask: %v", err)
	}
	if res.Minted == nil || res.Joined != "" {
		t.Fatalf("first escalation = %+v, want a minted task and no join", res)
	}
	design := res.Minted

	// 1. The lease is closed and the task is back in the ready set.
	if _, err := s.ActiveLease(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("active lease on %s after escalating: want ErrNotFound, got %v", first.ID, err)
	}
	got, err := s.GetTask(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetTask %s: %v", first.ID, err)
	}
	if got.State != "ready" {
		t.Errorf("%s is %s after escalating, want ready", first.ID, got.State)
	}

	// 2. The design task carries the section-scoped reference and the author.
	if design.Kind != "design" || design.Priority != "high" {
		t.Errorf("minted task is a %s at %s priority, want design at high", design.Kind, design.Priority)
	}
	if design.AboutDoc != plan.ID || design.AboutAnchor != "sec-3" {
		t.Errorf("minted task is about doc %d anchor %q, want %d sec-3",
			design.AboutDoc, design.AboutAnchor, plan.ID)
	}
	if design.Assignee != "alice" {
		t.Errorf("minted task assigned to %q, want alice (the plan's author)", design.Assignee)
	}
	// Read it back: the fields above must be stored, not just returned.
	stored, err := s.GetTask(ctx, design.ID)
	if err != nil {
		t.Fatalf("GetTask %s: %v", design.ID, err)
	}
	if stored.AboutAnchor != "sec-3" || stored.AboutDoc != plan.ID {
		t.Errorf("stored design task is about doc %d anchor %q, want %d sec-3",
			stored.AboutDoc, stored.AboutAnchor, plan.ID)
	}

	// 3. The blocks edge holds the escalating task until the fix lands.
	blocked, err := s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !blocked[first.ID] {
		t.Errorf("%s is not blocked after escalating", first.ID)
	}

	// 4. The event says what happened, naming its subject under "task".
	wantPayload(t, s, "task.gap_found", map[string]any{
		"task": first.ID, "doc": plan.Slug, "anchor": "sec-3", "to": "plan",
		"reason": "the plan says nothing about the empty case",
	})

	// 5. A second executor on the same section joins rather than minting.
	if _, err := s.Claim(ctx, second.ID, "stig", "wt-2", 0); err != nil {
		t.Fatalf("claim %s: %v", second.ID, err)
	}
	res2, err := s.EscalateTask(ctx, EscalateInput{
		TaskID: second.ID, To: "plan", DocID: plan.ID, Anchor: "sec-3",
		Reason: "same gap, other end", ActorID: "stig",
	})
	if err != nil {
		t.Fatalf("second EscalateTask: %v", err)
	}
	if res2.Minted != nil || res2.Joined != design.ID {
		t.Fatalf("second escalation = %+v, want joined %s", res2, design.ID)
	}
	designs, err := s.ListTasks(ctx, TaskFilter{Project: "horndb", Kind: "design"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(designs) != 1 {
		t.Fatalf("got %d design tasks, want 1: %v", len(designs), taskIDs(designs))
	}
	blocked, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !blocked[second.ID] {
		t.Errorf("%s is not blocked after joining the open escalation", second.ID)
	}
}

// TestEscalateTaskWithoutLease: escalating is an act of the executor holding
// the task. Someone who does not hold it gets ErrNotFound and writes nothing —
// no design task, no edge, no event.
func TestEscalateTaskWithoutLease(t *testing.T) {
	t.Parallel()
	s := openEscalateStore(t)
	ctx := t.Context()

	plan := mustCreateDoc(t, s, DocInput{
		Project: "horndb", Kind: "plan", Number: 8, Slug: "008-plan",
		Body: "---\nstatus: accepted\n---\n\n# A plan\n", CreatedBy: "alice",
	})
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	_, err := s.EscalateTask(ctx, EscalateInput{
		TaskID: task.ID, To: "plan", DocID: plan.ID,
		Reason: "not mine to escalate", ActorID: "stig",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("escalate without a lease: want ErrNotFound, got %v", err)
	}
	designs, err := s.ListTasks(ctx, TaskFilter{Project: "horndb", Kind: "design"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(designs) != 0 {
		t.Fatalf("a refused escalation minted %v", taskIDs(designs))
	}
}

// TestEscalateTaskRefusesBadInput: the store's own gate. The API resolves the
// target document, so an escalation reaching the store with none — a `--to
// spec` on a plan covering two specs, which the handler refuses to guess at —
// is invalid input rather than a 500.
func TestEscalateTaskRefusesBadInput(t *testing.T) {
	t.Parallel()
	s := openEscalateStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	for _, tc := range []struct {
		name string
		in   EscalateInput
	}{
		{"no document", EscalateInput{TaskID: task.ID, To: "spec", Reason: "why", ActorID: "stig"}},
		{"unknown tier", EscalateInput{TaskID: task.ID, To: "vibes", DocID: 1, Reason: "why", ActorID: "stig"}},
		{"blank reason", EscalateInput{TaskID: task.ID, To: "plan", DocID: 1, Reason: "  ", ActorID: "stig"}},
	} {
		if _, err := s.EscalateTask(ctx, tc.in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: want ErrInvalidInput, got %v", tc.name, err)
		}
	}
}
