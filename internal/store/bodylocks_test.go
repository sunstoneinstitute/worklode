package store

import (
	"errors"
	"testing"
	"time"

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
