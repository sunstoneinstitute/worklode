package store

import (
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEnqueueInstruction(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	instr, err := s.EnqueueInstruction(ctx, task.ID, "stig", "rebase onto main first")
	if err != nil {
		t.Fatalf("enqueue instruction: %v", err)
	}
	if instr.Task != task.ID || instr.Body != "rebase onto main first" || instr.CreatedBy != "stig" {
		t.Fatalf("instruction = %+v, want task=%s body=%q created_by=stig", instr, task.ID, "rebase onto main first")
	}
	if instr.ID == 0 {
		t.Fatal("instruction id is zero")
	}
	if instr.CreatedAt.IsZero() {
		t.Fatal("instruction created_at is zero")
	}
}

func TestEnqueueInstructionUnknownTask(t *testing.T) {
	s := openTaskStore(t)
	if _, err := s.EnqueueInstruction(t.Context(), "HDB-999", "stig", "body"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enqueue instruction on unknown task err = %v, want ErrNotFound", err)
	}
}

func TestEnqueueInstructionDeletedTask(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	_, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "task.deleted", nil,
		func(tx *sql.Tx, eventID int64) error {
			return DeleteTask(tx, leaseTestNow, task.ID, "stig", "", eventID)
		})
	if err != nil {
		t.Fatalf("delete task: %v", err)
	}

	if _, err := s.EnqueueInstruction(ctx, task.ID, "stig", "body"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enqueue instruction on deleted task err = %v, want ErrNotFound", err)
	}
}

func TestEnqueueInstructionUnknownActor(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.EnqueueInstruction(ctx, task.ID, "nobody", "body"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enqueue instruction from unknown actor err = %v, want ErrNotFound", err)
	}
}

// TestClaimPendingInstructionsForActor covers the actor-lease scoping: only
// instructions on tasks the actor currently leases are delivered, delivered
// rows are marked so a second claim finds nothing, and rows on a task the
// actor does not lease are left untouched.
func TestClaimPendingInstructionsForActor(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}

	leased := leaseForTest(t, s, "host:/wt-a") // held by stig
	unleased := createTask(t, s, leaseTestNow, defaultTaskInput())
	othersTask := createTask(t, s, leaseTestNow, defaultTaskInput())
	if _, err := s.Claim(ctx, othersTask.ID, "bob", "host:/wt-b", 0); err != nil {
		t.Fatalf("claim %s as bob: %v", othersTask.ID, err)
	}

	if _, err := s.EnqueueInstruction(ctx, leased.TaskID, "stig", "steer on leased task"); err != nil {
		t.Fatalf("enqueue on leased task: %v", err)
	}
	if _, err := s.EnqueueInstruction(ctx, unleased.ID, "stig", "steer on unleased task"); err != nil {
		t.Fatalf("enqueue on unleased task: %v", err)
	}
	if _, err := s.EnqueueInstruction(ctx, othersTask.ID, "stig", "steer on bob's task"); err != nil {
		t.Fatalf("enqueue on bob's task: %v", err)
	}

	got, err := s.ClaimPendingInstructionsForActor(ctx, "stig")
	if err != nil {
		t.Fatalf("claim pending instructions: %v", err)
	}
	if len(got) != 1 || got[0].Task != leased.TaskID {
		t.Fatalf("claimed = %+v, want exactly one instruction on %s", got, leased.TaskID)
	}

	// Re-claiming finds nothing: the row is already delivered.
	again, err := s.ClaimPendingInstructionsForActor(ctx, "stig")
	if err != nil || len(again) != 0 {
		t.Fatalf("re-claim = (%v, %v), want (0 rows, nil)", again, err)
	}

	// The instruction queued on the task stig does not lease is still
	// pending — untouched by stig's claim.
	pending := countPendingInstructions(t, s, unleased.ID)
	if pending != 1 {
		t.Fatalf("pending instructions on unleased task = %d, want 1", pending)
	}

	// bob claiming delivers the instruction on his own leased task.
	bobsGot, err := s.ClaimPendingInstructionsForActor(ctx, "bob")
	if err != nil {
		t.Fatalf("claim pending instructions for bob: %v", err)
	}
	if len(bobsGot) != 1 || bobsGot[0].Task != othersTask.ID {
		t.Fatalf("bob's claimed = %+v, want exactly one instruction on %s", bobsGot, othersTask.ID)
	}
}

// TestClaimPendingInstructionsForActorOrder asserts delivery order is sorted
// by id, since RETURNING order is unspecified.
func TestClaimPendingInstructionsForActorOrder(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt-a")

	var want []int64
	for i := 0; i < 5; i++ {
		instr, err := s.EnqueueInstruction(ctx, lease.TaskID, "stig", "step")
		if err != nil {
			t.Fatalf("enqueue instruction %d: %v", i, err)
		}
		want = append(want, instr.ID)
	}

	got, err := s.ClaimPendingInstructionsForActor(ctx, "stig")
	if err != nil {
		t.Fatalf("claim pending instructions: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("claimed %d instructions, want %d", len(got), len(want))
	}
	for i, instr := range got {
		if instr.ID != want[i] {
			t.Fatalf("claimed[%d].ID = %d, want %d (not sorted by id)", i, instr.ID, want[i])
		}
	}
}

func countPendingInstructions(t *testing.T, s *Store, taskID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM task_instructions WHERE task_id = $1 AND delivered_at IS NULL`, taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count pending instructions for %s: %v", taskID, err)
	}
	return n
}

// TestClaimPendingInstructionsRace fires n concurrent claims for the same
// actor against the same leased task's pending instructions: every row is
// delivered exactly once, with no duplicates and none left behind.
func TestClaimPendingInstructionsRace(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt-a")

	const rows = 20
	for i := 0; i < rows; i++ {
		if _, err := s.EnqueueInstruction(ctx, lease.TaskID, "stig", "step"); err != nil {
			t.Fatalf("enqueue instruction %d: %v", i, err)
		}
	}

	const workers = 8
	var delivered atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := s.ClaimPendingInstructionsForActor(ctx, "stig")
			if err != nil {
				t.Errorf("goroutine %d: claim pending instructions: %v", i, err)
				return
			}
			delivered.Add(int32(len(got)))
		}(i)
	}
	wg.Wait()

	if int(delivered.Load()) != rows {
		t.Fatalf("delivered = %d across all workers, want %d (no duplicates, none dropped)", delivered.Load(), rows)
	}
	if pending := countPendingInstructions(t, s, lease.TaskID); pending != 0 {
		t.Fatalf("pending instructions after race = %d, want 0", pending)
	}
}
