package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"
)

var leaseTestNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

// openLeaseStore opens a store with the task fixtures (project "horndb",
// actor "stig") and a controllable clock. Move the clock by assigning
// through the returned pointer.
func openLeaseStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s := openTaskStore(t)
	now := leaseTestNow
	s.SetNowFunc(func() time.Time { return now })
	return s, &now
}

// countLeases returns (active, total) lease rows for taskID.
func countLeases(t *testing.T, s *Store, taskID string) (active, total int) {
	t.Helper()
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM leases WHERE task_id = $1 AND released_at IS NULL`, taskID,
	).Scan(&active); err != nil {
		t.Fatalf("count active leases: %v", err)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM leases WHERE task_id = $1`, taskID,
	).Scan(&total); err != nil {
		t.Fatalf("count leases: %v", err)
	}
	return active, total
}

func mustState(t *testing.T, s *Store, taskID, want string) {
	t.Helper()
	got, err := s.GetTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("GetTask %s: %v", taskID, err)
	}
	if got.State != want {
		t.Fatalf("task %s state: got %q, want %q", taskID, got.State, want)
	}
}

func TestClaimExactlyOnce(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	const workers = 16
	errs := make([]error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := s.Claim(ctx, task.ID, "stig", fmt.Sprintf("host:/wt-%d", i), DefaultLeaseTTL)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	var wins, leased int
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrLeased):
			leased++
		default:
			t.Fatalf("goroutine %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 || leased != workers-1 {
		t.Fatalf("claims: %d succeeded, %d ErrLeased; want 1 and %d", wins, leased, workers-1)
	}

	mustState(t, s, task.ID, "in_progress")
	active, total := countLeases(t, s, task.ID)
	if active != 1 || total != 1 {
		t.Fatalf("lease rows: active=%d total=%d, want 1 and 1", active, total)
	}
}

func TestClaimSecondWorktreeSameTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-a", 0); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	// A second claim on the same task from a different worktree is refused.
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-b", 0); !errors.Is(err, ErrLeased) {
		t.Fatalf("second claim, different worktree: want ErrLeased, got %v", err)
	}
	active, total := countLeases(t, s, task.ID)
	if active != 1 || total != 1 {
		t.Fatalf("lease rows: active=%d total=%d, want 1 and 1", active, total)
	}
}

func TestClaimSameWorktreeSecondTask(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task1 := createTask(t, s, leaseTestNow, defaultTaskInput())
	task2 := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task1.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim task1: %v", err)
	}
	// One actor's worktree cannot hold active leases on two tasks: the
	// leases_active_worktree unique index fires and maps to ErrLeased.
	if _, err := s.Claim(ctx, task2.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrLeased) {
		t.Fatalf("claim second task from same worktree: want ErrLeased, got %v", err)
	}
	mustState(t, s, task2.ID, "ready")
	if _, total := countLeases(t, s, task2.ID); total != 0 {
		t.Fatalf("task2: %d lease rows after refused claim, want 0", total)
	}

	// Releasing the first lease frees the worktree for the second task.
	if err := s.Release(ctx, task1.ID, "stig"); err != nil {
		t.Fatalf("Release task1: %v", err)
	}
	if _, err := s.Claim(ctx, task2.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("claim task2 after release: %v", err)
	}
}

// TestClaimSameWorktreePathDifferentActors pins the scope of the worktree
// index: it stops one actor working two tasks from one directory, and says
// nothing about two actors whose worktree identities happen to collide. The
// identity is "<hostname>:<path>", so a collision means two operators sharing
// a hostname — devcontainers, a shared dev box, identically-named pods — and
// scoping the index by actor is what stops one of them seeing "worktree
// already holds an active lease" for a task nobody on their machine claimed.
func TestClaimSameWorktreePathDifferentActors(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	task1 := createTask(t, s, leaseTestNow, defaultTaskInput())
	task2 := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task1.ID, "stig", "devbox:/src/worklode", 0); err != nil {
		t.Fatalf("Claim task1 as stig: %v", err)
	}
	if _, err := s.Claim(ctx, task2.ID, "bob", "devbox:/src/worklode", 0); err != nil {
		t.Fatalf("Claim task2 as bob on the same worktree identity: %v", err)
	}
	mustState(t, s, task2.ID, "in_progress")

	// Bob is still held to one task per worktree.
	task3 := createTask(t, s, leaseTestNow, defaultTaskInput())
	if _, err := s.Claim(ctx, task3.ID, "bob", "devbox:/src/worklode", 0); !errors.Is(err, ErrLeased) {
		t.Fatalf("bob claiming a third task from his worktree: want ErrLeased, got %v", err)
	}
}

// TestRebindWorktreeDifferentActors is RebindLeaseWorktree's half of the same
// scope: rebinding onto a path another actor holds is allowed, onto one the
// rebinding actor already holds is not.
func TestRebindWorktreeDifferentActors(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	task1 := createTask(t, s, leaseTestNow, defaultTaskInput())
	task2 := createTask(t, s, leaseTestNow, defaultTaskInput())
	task3 := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task1.ID, "stig", "devbox:/wt-stig", 0); err != nil {
		t.Fatalf("Claim task1 as stig: %v", err)
	}
	if _, err := s.Claim(ctx, task2.ID, "bob", "devbox:/wt-bob", 0); err != nil {
		t.Fatalf("Claim task2 as bob: %v", err)
	}
	if _, err := s.Claim(ctx, task3.ID, "bob", "devbox:/wt-bob-2", 0); err != nil {
		t.Fatalf("Claim task3 as bob: %v", err)
	}

	// Onto a path stig holds: allowed, they are different actors.
	if _, err := s.RebindLeaseWorktree(ctx, task2.ID, "bob", "devbox:/wt-stig"); err != nil {
		t.Fatalf("rebind onto another actor's worktree path: %v", err)
	}
	// Onto a path bob himself holds: refused.
	if _, err := s.RebindLeaseWorktree(ctx, task3.ID, "bob", "devbox:/wt-stig"); !errors.Is(err, ErrLeased) {
		t.Fatalf("rebind onto own held worktree: want ErrLeased, got %v", err)
	}
}

func TestClaimRequiresReady(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	draftIn := defaultTaskInput()
	draftIn.Draft = true
	draft := createTask(t, s, leaseTestNow, draftIn)
	if _, err := s.Claim(ctx, draft.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("claim draft task: want ErrBadTransition, got %v", err)
	}

	merged := createTask(t, s, leaseTestNow, defaultTaskInput())
	walkTo(t, s, merged.ID, "merged")
	if _, err := s.Claim(ctx, merged.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("claim merged task: want ErrBadTransition, got %v", err)
	}

	blocker := createTask(t, s, leaseTestNow, defaultTaskInput()) // stays ready
	blocked := createTask(t, s, leaseTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	if _, err := s.Claim(ctx, blocked.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("claim blocked task: want ErrBlocked, got %v", err)
	}

	// Failed claims leave no lease rows behind.
	for _, id := range []string{draft.ID, merged.ID, blocked.ID} {
		if _, total := countLeases(t, s, id); total != 0 {
			t.Fatalf("task %s: %d lease rows after failed claim, want 0", id, total)
		}
	}
}

// TestClaimRejectsDecision pins 004 §6.3 as amended (WL-638): a decision is
// never leased, even by a direct claim by id (the human_only escape hatch
// does not apply here). `lode task assign` is the intended path instead.
func TestClaimRejectsDecision(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	in := defaultTaskInput()
	in.Kind = "decision"
	task := createTask(t, s, leaseTestNow, in)

	if _, err := s.Claim(t.Context(), task.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("claim decision task: want ErrBadTransition, got %v", err)
	}
	if active, total := countLeases(t, s, task.ID); active != 0 || total != 0 {
		t.Fatalf("decision task %s: %d/%d leases after rejected claim, want 0/0", task.ID, active, total)
	}
}

func TestClaimUnknownTaskOrActor(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, "HDB-999", "stig", "host:/wt", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim unknown task: want ErrNotFound, got %v", err)
	}
	if _, err := s.Claim(ctx, task.ID, "ghost", "host:/wt", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim by unknown actor: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "ready")
}

func TestRenewRelease(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "agent", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	lease, err := s.Claim(ctx, task.ID, "stig", "host:/wt-1", 2*time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !lease.AcquiredAt.Equal(leaseTestNow) || !lease.RenewedAt.Equal(leaseTestNow) ||
		!lease.ExpiresAt.Equal(leaseTestNow.Add(2*time.Hour)) {
		t.Fatalf("claimed lease times: %+v", lease)
	}
	if lease.Worktree != "host:/wt-1" {
		t.Fatalf("claimed lease worktree: got %q, want host:/wt-1", lease.Worktree)
	}

	// Renew by the holder extends expires_at and sets renewed_at.
	*now = leaseTestNow.Add(30 * time.Minute)
	renewed, err := s.Renew(ctx, task.ID, "stig", time.Hour)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if !renewed.RenewedAt.Equal(*now) || !renewed.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("renewed lease: renewed_at=%v expires_at=%v, want %v and %v",
			renewed.RenewedAt, renewed.ExpiresAt, *now, now.Add(time.Hour))
	}
	got, err := s.ActiveLease(ctx, task.ID)
	if err != nil {
		t.Fatalf("ActiveLease: %v", err)
	}
	if got.ID != lease.ID || !got.RenewedAt.Equal(*now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ActiveLease after renew: %+v", got)
	}
	if got.Worktree != "host:/wt-1" {
		t.Fatalf("ActiveLease worktree: got %q, want host:/wt-1", got.Worktree)
	}

	// A non-holder gets ErrNotFound (documented choice: no active lease *for
	// that actor* — does not reveal who does hold it).
	if _, err := s.Renew(ctx, task.ID, "bob", time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Renew by non-holder: want ErrNotFound, got %v", err)
	}
	if err := s.Release(ctx, task.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Release by non-holder: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "in_progress") // untouched by the failed calls

	// Release by the holder closes the lease and moves the task back to ready.
	*now = leaseTestNow.Add(40 * time.Minute)
	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := s.ActiveLease(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveLease after release: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "ready")
	var releasedAt time.Time
	if err := s.db.QueryRow(`SELECT released_at FROM leases WHERE id = $1`, lease.ID).Scan(&releasedAt); err != nil {
		t.Fatalf("read released_at: %v", err)
	}
	if !releasedAt.UTC().Equal(*now) {
		t.Fatalf("released_at: got %v, want %v", releasedAt.UTC(), *now)
	}

	// Releasing after the task already left in_progress closes the lease
	// without touching task state.
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-2", 2*time.Hour); err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if err := transition(t, s, *now, task.ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("transition to in_review: %v", err)
	}
	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("Release with task in_review: %v", err)
	}
	if _, err := s.ActiveLease(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveLease after second release: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "in_review")

	// Renew with no active lease at all → ErrNotFound.
	if _, err := s.Renew(ctx, task.ID, "stig", time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Renew without lease: want ErrNotFound, got %v", err)
	}
}

// TestLeaseEventTypesPastTense pins the recorded event types for the lease
// lifecycle: past tense ("lease.claimed"), matching every other event type.
func TestLeaseEventTypesPastTense(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := s.Renew(ctx, task.ID, "stig", 0); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	for _, typ := range []string{"lease.claimed", "lease.renewed", "lease.released"} {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM events WHERE source = 'cli' AND type = $1`, typ,
		).Scan(&n); err != nil {
			t.Fatalf("count %s events: %v", typ, err)
		}
		if n != 1 {
			t.Errorf("%s events: got %d, want 1", typ, n)
		}
	}
}

func TestRebindLeaseWorktree(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "agent", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	task := createTask(t, s, leaseTestNow, defaultTaskInput())
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt-1", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// The holder rebinds to a new worktree; the returned lease and a fresh
	// ActiveLease read both reflect the change.
	rebound, err := s.RebindLeaseWorktree(ctx, task.ID, "stig", "host:/wt-moved")
	if err != nil {
		t.Fatalf("RebindLeaseWorktree by holder: %v", err)
	}
	if rebound.Worktree != "host:/wt-moved" {
		t.Fatalf("returned lease worktree = %q, want host:/wt-moved", rebound.Worktree)
	}
	got, err := s.ActiveLease(ctx, task.ID)
	if err != nil {
		t.Fatalf("ActiveLease: %v", err)
	}
	if got.Worktree != "host:/wt-moved" {
		t.Fatalf("worktree after rebind = %q, want host:/wt-moved", got.Worktree)
	}

	// A non-holder gets ErrNotFound, indistinguishable from no lease at all.
	if _, err := s.RebindLeaseWorktree(ctx, task.ID, "bob", "host:/wt-bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rebind by non-holder: want ErrNotFound, got %v", err)
	}
	if got, _ := s.ActiveLease(ctx, task.ID); got.Worktree != "host:/wt-moved" {
		t.Fatalf("worktree changed by failed non-holder rebind: %q", got.Worktree)
	}

	// Rebinding onto a worktree that already holds another active lease fires
	// the leases_active_worktree unique index and maps to ErrLeased.
	task2 := createTask(t, s, leaseTestNow, defaultTaskInput())
	if _, err := s.Claim(ctx, task2.ID, "stig", "host:/wt-other", 0); err != nil {
		t.Fatalf("Claim task2: %v", err)
	}
	if _, err := s.RebindLeaseWorktree(ctx, task.ID, "stig", "host:/wt-other"); !errors.Is(err, ErrLeased) {
		t.Fatalf("rebind onto taken worktree: want ErrLeased, got %v", err)
	}
	if got, _ := s.ActiveLease(ctx, task.ID); got.Worktree != "host:/wt-moved" {
		t.Fatalf("worktree changed by refused rebind: %q", got.Worktree)
	}

	// Rebind with no active lease at all → ErrNotFound.
	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := s.RebindLeaseWorktree(ctx, task.ID, "stig", "host:/wt-x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rebind without lease: want ErrNotFound, got %v", err)
	}
}

func TestSweeper(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	lease, err := s.Claim(ctx, task.ID, "stig", "host:/wt-1", 2*time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	*now = leaseTestNow.Add(3 * time.Hour)
	n, err := s.ExpireLeases(ctx, s.nowFn())
	if err != nil {
		t.Fatalf("ExpireLeases: %v", err)
	}
	if n != 1 {
		t.Fatalf("ExpireLeases: got %d, want 1", n)
	}
	if _, err := s.ActiveLease(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveLease after expiry: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "ready")

	// A system-source "lease.expired" event exists with the idempotency id.
	extID := fmt.Sprintf("lease-expired-%d", lease.ID)
	var eventID int64
	var typ string
	if err := s.db.QueryRow(
		`SELECT id, type FROM events WHERE source = 'system' AND external_id = $1`, extID,
	).Scan(&eventID, &typ); err != nil {
		t.Fatalf("read system event %s: %v", extID, err)
	}
	if typ != "lease.expired" {
		t.Fatalf("system event type: got %q, want lease.expired", typ)
	}

	// The in_progress→ready transition is in state_log, attributed to that event.
	var changeJSON string
	if err := s.db.QueryRow(
		`SELECT change FROM state_log WHERE entity_id = $1 AND event_id = $2`, task.ID, eventID,
	).Scan(&changeJSON); err != nil {
		t.Fatalf("read state_log for event %d: %v", eventID, err)
	}
	var change map[string]string
	if err := json.Unmarshal([]byte(changeJSON), &change); err != nil {
		t.Fatalf("unmarshal change %q: %v", changeJSON, err)
	}
	if change["field"] != "state" || change["old"] != "in_progress" || change["new"] != "ready" {
		t.Fatalf("state_log change: got %v, want state in_progress -> ready", change)
	}

	// Idempotent: a second sweep finds nothing.
	if n, err := s.ExpireLeases(ctx, s.nowFn()); err != nil || n != 0 {
		t.Fatalf("second ExpireLeases: got (%d, %v), want (0, nil)", n, err)
	}

	// A lease whose task already left in_progress is still closed on expiry,
	// but the task state is untouched.
	task2 := createTask(t, s, *now, defaultTaskInput())
	lease2, err := s.Claim(ctx, task2.ID, "stig", "host:/wt-2", 2*time.Hour)
	if err != nil {
		t.Fatalf("Claim task2: %v", err)
	}
	if err := transition(t, s, *now, task2.ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("transition task2 to in_review: %v", err)
	}
	*now = now.Add(3 * time.Hour)
	if n, err := s.ExpireLeases(ctx, s.nowFn()); err != nil || n != 1 {
		t.Fatalf("ExpireLeases task2: got (%d, %v), want (1, nil)", n, err)
	}
	mustState(t, s, task2.ID, "in_review")
	var releasedAt sql.NullTime
	if err := s.db.QueryRow(`SELECT released_at FROM leases WHERE id = $1`, lease2.ID).Scan(&releasedAt); err != nil {
		t.Fatalf("read task2 lease released_at: %v", err)
	}
	if !releasedAt.Valid {
		t.Fatalf("task2 lease still open after expiry")
	}
}

// TestSweeperConcurrentReplicas runs two sweeps at once from two Stores on
// the same database (two replicas). The advisory lock plus the idempotent
// per-lease events must yield exactly one lease.expired event per lease,
// with the swept counts summing to the number of expired leases.
func TestSweeperConcurrentReplicas(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()

	const numLeases = 8
	leaseIDs := make([]int64, 0, numLeases)
	for i := range numLeases {
		task := createTask(t, s, leaseTestNow, defaultTaskInput())
		lease, err := s.Claim(ctx, task.ID, "stig", fmt.Sprintf("host:/wt-sweep-%d", i), 2*time.Hour)
		if err != nil {
			t.Fatalf("Claim %d: %v", i, err)
		}
		leaseIDs = append(leaseIDs, lease.ID)
	}
	*now = leaseTestNow.Add(3 * time.Hour)

	// Second store on the same test database simulates a second replica.
	var dbName string
	if err := s.db.QueryRow(`SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	u, err := url.Parse(TestDSN())
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	u.Path = "/" + dbName
	s2, err := Open(u.String())
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	stores := []*Store{s, s2}
	counts := make([]int, len(stores))
	errs := make([]error, len(stores))
	var wg sync.WaitGroup
	for i, st := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counts[i], errs[i] = st.ExpireLeases(ctx, *now)
		}()
	}
	wg.Wait()

	total := 0
	for i := range stores {
		if errs[i] != nil {
			t.Fatalf("ExpireLeases replica %d: %v", i, errs[i])
		}
		total += counts[i]
	}
	// One replica may lose the lock race and return 0; any split is fine
	// as long as every lease was swept exactly once in total.
	if total != numLeases {
		t.Fatalf("swept counts %v sum to %d, want %d", counts, total, numLeases)
	}

	for _, id := range leaseIDs {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM events WHERE source = 'system' AND type = 'lease.expired' AND external_id = $1`,
			fmt.Sprintf("lease-expired-%d", id),
		).Scan(&n); err != nil {
			t.Fatalf("count events for lease %d: %v", id, err)
		}
		if n != 1 {
			t.Fatalf("lease %d: got %d lease.expired events, want 1", id, n)
		}
	}
	var eventTotal int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE type = 'lease.expired'`,
	).Scan(&eventTotal); err != nil {
		t.Fatalf("count lease.expired events: %v", err)
	}
	if eventTotal != numLeases {
		t.Fatalf("total lease.expired events: got %d, want %d", eventTotal, numLeases)
	}
}
