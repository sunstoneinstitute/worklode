package store

import (
	"errors"
	"testing"
	"time"
)

// countLiveTaskTokens returns unrevoked token rows bound to taskID.
func countLiveTaskTokens(t *testing.T, s *Store, taskID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM tokens WHERE task_id = $1 AND revoked_at IS NULL`, taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count task tokens: %v", err)
	}
	return n
}

func TestTaskTokenAuthenticatesWithBinding(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, *now, defaultTaskInput())

	plaintext, err := s.CreateTaskToken(ctx, "stig", task.ID, "sandbox token", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateTaskToken: %v", err)
	}
	a, boundTask, err := s.Authenticate(ctx, plaintext)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if a.ID != "stig" {
		t.Fatalf("actor: got %q, want stig", a.ID)
	}
	if boundTask != task.ID {
		t.Fatalf("bound task: got %q, want %q", boundTask, task.ID)
	}

	// An ordinary actor token stays unbound.
	plain2, err := s.CreateToken(ctx, "stig", "plain", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, boundTask, err = s.Authenticate(ctx, plain2); err != nil || boundTask != "" {
		t.Fatalf("plain token: bound %q, err %v; want unbound, nil", boundTask, err)
	}
}

func TestTaskTokenRevokedWhenLeaseCloses(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, *now, defaultTaskInput())
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", DefaultLeaseTTL); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	plaintext, err := s.CreateTaskToken(ctx, "stig", task.ID, "sandbox token", now.Add(DefaultLeaseTTL))
	if err != nil {
		t.Fatalf("CreateTaskToken: %v", err)
	}

	if err := s.Release(ctx, task.ID, "stig"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if n := countLiveTaskTokens(t, s, task.ID); n != 0 {
		t.Fatalf("live task tokens after release: got %d, want 0", n)
	}
	if _, _, err := s.Authenticate(ctx, plaintext); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate after release: got %v, want ErrNotFound", err)
	}
}

func TestTaskTokenRevokedWhenLeaseExpires(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, *now, defaultTaskInput())
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", DefaultLeaseTTL); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	plaintext, err := s.CreateTaskToken(ctx, "stig", task.ID, "sandbox token", now.Add(DefaultLeaseTTL))
	if err != nil {
		t.Fatalf("CreateTaskToken: %v", err)
	}

	*now = now.Add(DefaultLeaseTTL + time.Minute)
	if _, err := s.ExpireLeases(ctx, *now); err != nil {
		t.Fatalf("ExpireLeases: %v", err)
	}
	if n := countLiveTaskTokens(t, s, task.ID); n != 0 {
		t.Fatalf("live task tokens after lease expiry: got %d, want 0", n)
	}
	if _, _, err := s.Authenticate(ctx, plaintext); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authenticate after lease expiry: got %v, want ErrNotFound", err)
	}
}

func TestTaskTokenExtendedByRenew(t *testing.T) {
	t.Parallel()
	s, now := openLeaseStore(t)
	ctx := t.Context()
	task := createTask(t, s, *now, defaultTaskInput())
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", DefaultLeaseTTL); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := s.CreateTaskToken(ctx, "stig", task.ID, "sandbox token", now.Add(DefaultLeaseTTL)); err != nil {
		t.Fatalf("CreateTaskToken: %v", err)
	}
	firstExpiry := now.Add(DefaultLeaseTTL)

	*now = now.Add(30 * time.Minute)
	lease, err := s.Renew(ctx, task.ID, "stig", DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}

	var tokenExpiry time.Time
	if err := s.db.QueryRow(
		`SELECT expires_at FROM tokens WHERE task_id = $1 AND revoked_at IS NULL`, task.ID,
	).Scan(&tokenExpiry); err != nil {
		t.Fatalf("read token expiry: %v", err)
	}
	if !tokenExpiry.After(firstExpiry) {
		t.Fatalf("token expiry not extended: %v (was %v)", tokenExpiry, firstExpiry)
	}
	if !tokenExpiry.Equal(lease.ExpiresAt) {
		t.Fatalf("token expiry %v does not track lease expiry %v", tokenExpiry, lease.ExpiresAt)
	}
}

func TestEnsureActorIdempotent(t *testing.T) {
	t.Parallel()
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	for range 2 {
		if err := s.EnsureActor(ctx, "sandbox", "agent", "Sandbox worker"); err != nil {
			t.Fatalf("EnsureActor: %v", err)
		}
	}
	a, err := s.GetActor(ctx, "sandbox")
	if err != nil {
		t.Fatalf("GetActor: %v", err)
	}
	if a.Kind != "agent" || a.Admin {
		t.Fatalf("sandbox actor: kind %q admin %v; want agent, non-admin", a.Kind, a.Admin)
	}
}
