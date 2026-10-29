package store

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestTakeLockSecondHolderRefused(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	a, err := s.TakeLock(ctx, "rule", "7", "alice")
	if err != nil || a.Session == "" {
		t.Fatalf("alice: %v %+v", err, a)
	}
	_, err = s.TakeLock(ctx, "rule", "7", "bob")
	var held *ErrLockHeld
	if !errors.As(err, &held) || held.Lock.Holder != "alice" {
		t.Fatalf("bob: %v", err)
	}
	b, err := s.TakeLock(ctx, "rule", "7", "alice") // second tab
	if err != nil || b.Session == a.Session {
		t.Fatalf("second tab: %v %+v", err, b)
	}
	if _, err := s.TakeLock(ctx, "nope", "7", "alice"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad kind: %v", err)
	}
}

func TestLockCheckInReleaseUnlock(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	a, _ := s.TakeLock(ctx, "doc", "1", "alice")
	b, _ := s.TakeLock(ctx, "doc", "1", "alice")

	l, err := s.CheckInLock(ctx, "doc", "1", model.LockCheckIn{Session: a.Session, Dirty: true, LastActivity: time.Now()})
	if err != nil || l.Holder != "alice" {
		t.Fatalf("check in: %v %+v", err, l)
	}
	if err := s.ReleaseLockSession(ctx, "doc", "1", a.Session); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.GetLock(ctx, "doc", "1"); l == nil {
		t.Fatal("lock gone while a second session lives")
	}
	_, err = s.CheckInLock(ctx, "doc", "1", model.LockCheckIn{Session: a.Session})
	var lost *ErrLockLost
	if !errors.As(err, &lost) || lost.Lost.Reason != "released" || lost.Lost.Lock.Holder != "alice" {
		t.Fatalf("released session: %v", err)
	}

	if err := s.UnlockBody(ctx, "doc", "1", "admin1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.GetLock(ctx, "doc", "1"); l != nil {
		t.Fatalf("lock survives unlock: %+v", l)
	}
	_, err = s.CheckInLock(ctx, "doc", "1", model.LockCheckIn{Session: b.Session})
	if !errors.As(err, &lost) || lost.Lost.Reason != "unlocked" || lost.Lost.By != "admin1" {
		t.Fatalf("unlocked session: %v", err)
	}
	if _, err := s.CheckInLock(ctx, "doc", "1", model.LockCheckIn{Session: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	if _, err := s.TakeLock(ctx, "doc", "1", "bob"); err != nil {
		t.Fatalf("bob after unlock: %v", err)
	}
}

func TestSweepLocks(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	now := time.Now()
	take := func(id string, dirty bool, active, checkedIn time.Duration) model.BodyLock {
		l, err := s.TakeLock(ctx, "task", id, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE body_lock_sessions SET dirty = $2, last_activity = $3, checked_in_at = $4 WHERE id = $1`,
			l.Session, dirty, now.Add(-active), now.Add(-checkedIn)); err != nil {
			t.Fatal(err)
		}
		return l
	}
	reason := func(l model.BodyLock) string {
		var r *string
		if err := s.db.QueryRowContext(ctx, `SELECT ended_reason FROM body_lock_sessions WHERE id = $1`, l.Session).Scan(&r); err != nil {
			t.Fatal(err)
		}
		if r == nil {
			return ""
		}
		return *r
	}
	clean := take("clean", false, 61*time.Minute, time.Minute)
	dirty := take("dirty", true, 61*time.Minute, time.Minute)
	old := take("old", true, time.Minute, 24*time.Hour+time.Minute)
	fresh := take("fresh", false, time.Minute, time.Minute)

	inactive, expired, err := s.SweepLocks(ctx, now)
	if err != nil || inactive != 1 || expired != 1 {
		t.Fatalf("sweep: %d %d %v", inactive, expired, err)
	}
	for _, c := range []struct {
		l    model.BodyLock
		want string
	}{{clean, "inactive"}, {dirty, ""}, {old, "expired"}, {fresh, ""}} {
		if got := reason(c.l); got != c.want {
			t.Errorf("%s: reason %q, want %q", c.l.SubjectID, got, c.want)
		}
		l, _ := s.GetLock(ctx, "task", c.l.SubjectID)
		if (c.want == "") != (l != nil) {
			t.Errorf("%s: lock present=%v with reason %q", c.l.SubjectID, l != nil, c.want)
		}
	}
}

func TestBodyLockMetrics(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := OpenTestStore(t, WithMetrics(reg))
	ctx := t.Context()
	now := time.Now()
	ev := func(kind, event string) float64 {
		return testutil.ToFloat64(s.metrics.bodyLockEvents.WithLabelValues(kind, event))
	}
	held := func(kind string) float64 {
		return testutil.ToFloat64(s.metrics.bodyLocksHeld.WithLabelValues(kind))
	}
	age := func(session string, checkedIn time.Duration) {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE body_lock_sessions SET checked_in_at = $2 WHERE id = $1`, session, now.Add(-checkedIn)); err != nil {
			t.Fatal(err)
		}
	}

	a, _ := s.TakeLock(ctx, "doc", "3", "alice")
	b, _ := s.TakeLock(ctx, "doc", "4", "alice")
	c, _ := s.TakeLock(ctx, "rule", "5", "alice")
	if _, err := s.TakeLock(ctx, "task", "6", "alice"); err != nil {
		t.Fatal(err)
	}
	if got := ev("doc", "take"); got != 2 {
		t.Fatalf("doc take = %v, want 2", got)
	}
	if err := s.ReleaseLockSession(ctx, "rule", "5", c.Session); err != nil {
		t.Fatal(err)
	}
	if got := ev("rule", "release"); got != 1 {
		t.Fatalf("rule release = %v, want 1", got)
	}
	if err := s.UnlockBody(ctx, "doc", "4", "admin"); err != nil {
		t.Fatal(err)
	}
	if got := ev("doc", "unlock"); got != 1 {
		t.Fatalf("doc unlock = %v, want 1", got)
	}
	_ = b

	age(a.Session, 25*time.Hour)
	if _, _, err := s.SweepLocks(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := ev("doc", "expired"); got != 1 {
		t.Fatalf("doc expired = %v, want 1", got)
	}
	for kind, want := range map[string]float64{"doc": 0, "rule": 0, "task": 1} {
		if got := held(kind); got != want {
			t.Errorf("held{%s} = %v, want %v", kind, got, want)
		}
	}

	// An idle clean session counts as inactive.
	l, _ := s.TakeLock(ctx, "task", "7", "bob")
	if _, err := s.db.ExecContext(ctx,
		`UPDATE body_lock_sessions SET last_activity = $2 WHERE id = $1`, l.Session, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SweepLocks(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := ev("task", "inactive"); got != 1 {
		t.Fatalf("task inactive = %v, want 1", got)
	}
	if got := held("task"); got != 1 {
		t.Fatalf("held{task} = %v, want 1", got)
	}
}

func TestBodySaveConflictsRegistered(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	s := OpenTestStore(t, WithMetrics(reg))
	s.BodySaveConflict("doc", "moved")
	s.BodySaveConflict("rule", "lock")
	if got := testutil.ToFloat64(s.metrics.bodySaveConflicts.WithLabelValues("doc", "moved")); got != 1 {
		t.Fatalf("doc moved = %v, want 1", got)
	}
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range fams {
		seen[f.GetName()] = true
	}
	for _, n := range []string{"worklode_body_locks_held", "worklode_body_lock_events_total", "worklode_body_save_conflicts_total"} {
		if !seen[n] {
			t.Errorf("%s not registered", n)
		}
	}
	// Nil-safe without WithMetrics.
	openTestStore(t).BodySaveConflict("doc", "lock")
}

// TestBodySaveLockCheck: a save with a stale lock session is ErrLockHeld; a
// save with no session succeeds while another actor holds the lock
// (WL-REQ-1868).
func TestBodySaveLockCheck(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	r, err := s.AddRule(ctx, model.AddRuleInput{Project: "p1", Heading: "H", Body: "v1\n"}, "stig")
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(r.ID, 10)
	mine, err := s.TakeLock(ctx, "rule", id, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseLockSession(ctx, "rule", id, mine.Session); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakeLock(ctx, "rule", id, "bob"); err != nil {
		t.Fatal(err)
	}
	_, err = s.EditRule(ctx, "P1", r.Number, model.EditRuleInput{Heading: "H", Body: "mine\n", LockSession: mine.Session}, "alice")
	var held *ErrLockHeld
	if !errors.As(err, &held) || held.Lock.Holder != "bob" {
		t.Fatalf("stale session: %v", err)
	}
	if _, err := s.EditRule(ctx, "P1", r.Number, model.EditRuleInput{Heading: "H", Body: "agent\n"}, "stig"); err != nil {
		t.Fatalf("no session while bob holds the lock: %v", err)
	}
}
