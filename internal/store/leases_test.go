package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
		`SELECT COUNT(*) FROM leases WHERE task_id = ? AND released_at IS NULL`, taskID,
	).Scan(&active); err != nil {
		t.Fatalf("count active leases: %v", err)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM leases WHERE task_id = ?`, taskID,
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
			_, err := s.Claim(ctx, task.ID, "stig", fmt.Sprintf("session-%d", i), DefaultLeaseTTL)
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

func TestClaimRequiresReady(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	draftIn := defaultTaskInput()
	draftIn.Draft = true
	draft := createTask(t, s, leaseTestNow, draftIn)
	if _, err := s.Claim(ctx, draft.ID, "stig", "sess", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("claim draft task: want ErrBadTransition, got %v", err)
	}

	done := createTask(t, s, leaseTestNow, defaultTaskInput())
	walkTo(t, s, done.ID, "done")
	if _, err := s.Claim(ctx, done.ID, "stig", "sess", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("claim done task: want ErrBadTransition, got %v", err)
	}

	blocker := createTask(t, s, leaseTestNow, defaultTaskInput()) // stays ready
	blocked := createTask(t, s, leaseTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	if _, err := s.Claim(ctx, blocked.ID, "stig", "sess", 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("claim blocked task: want ErrBlocked, got %v", err)
	}

	// Failed claims leave no lease rows behind.
	for _, id := range []string{draft.ID, done.ID, blocked.ID} {
		if _, total := countLeases(t, s, id); total != 0 {
			t.Fatalf("task %s: %d lease rows after failed claim, want 0", id, total)
		}
	}
}

func TestClaimUnknownTaskOrActor(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, "WT-999", "stig", "sess", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim unknown task: want ErrNotFound, got %v", err)
	}
	if _, err := s.Claim(ctx, task.ID, "ghost", "sess", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim by unknown actor: want ErrNotFound, got %v", err)
	}
	mustState(t, s, task.ID, "ready")
}

func TestRenewRelease(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	if err := s.CreateActor(ctx, "bob", "agent", "Bob", false); err != nil {
		t.Fatalf("CreateActor bob: %v", err)
	}
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	lease, err := s.Claim(ctx, task.ID, "stig", "sess-1", 2*time.Hour)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !lease.AcquiredAt.Equal(leaseTestNow) || !lease.RenewedAt.Equal(leaseTestNow) ||
		!lease.ExpiresAt.Equal(leaseTestNow.Add(2*time.Hour)) {
		t.Fatalf("claimed lease times: %+v", lease)
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
	var releasedAt string
	if err := s.db.QueryRow(`SELECT released_at FROM leases WHERE id = ?`, lease.ID).Scan(&releasedAt); err != nil {
		t.Fatalf("read released_at: %v", err)
	}
	if want := now.Format(time.RFC3339); releasedAt != want {
		t.Fatalf("released_at: got %q, want %q", releasedAt, want)
	}

	// Releasing after the task already left in_progress closes the lease
	// without touching task state.
	if _, err := s.Claim(ctx, task.ID, "stig", "sess-2", 2*time.Hour); err != nil {
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
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task.ID, "stig", "sess", 0); err != nil {
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
			`SELECT COUNT(*) FROM events WHERE source = 'cli' AND type = ?`, typ,
		).Scan(&n); err != nil {
			t.Fatalf("count %s events: %v", typ, err)
		}
		if n != 1 {
			t.Errorf("%s events: got %d, want 1", typ, n)
		}
	}
}

func TestSweeper(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())

	lease, err := s.Claim(ctx, task.ID, "stig", "sess", 2*time.Hour)
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
		`SELECT id, type FROM events WHERE source = 'system' AND external_id = ?`, extID,
	).Scan(&eventID, &typ); err != nil {
		t.Fatalf("read system event %s: %v", extID, err)
	}
	if typ != "lease.expired" {
		t.Fatalf("system event type: got %q, want lease.expired", typ)
	}

	// The in_progress→ready transition is in state_log, attributed to that event.
	var changeJSON string
	if err := s.db.QueryRow(
		`SELECT change FROM state_log WHERE entity_id = ? AND event_id = ?`, task.ID, eventID,
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
	lease2, err := s.Claim(ctx, task2.ID, "stig", "sess-2", 2*time.Hour)
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
	var releasedAt sql.NullString
	if err := s.db.QueryRow(`SELECT released_at FROM leases WHERE id = ?`, lease2.ID).Scan(&releasedAt); err != nil {
		t.Fatalf("read task2 lease released_at: %v", err)
	}
	if !releasedAt.Valid {
		t.Fatalf("task2 lease still open after expiry")
	}
}
