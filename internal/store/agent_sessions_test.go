package store

import (
	"strings"
	"testing"
	"time"
)

// insertRawSession inserts directly into agent_sessions, bypassing the store
// API, so schema constraints can be asserted on their own.
func insertRawSession(t *testing.T, s *Store, leaseID int64, agent, sessionID string) error {
	t.Helper()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	_, err := s.db.Exec(
		`INSERT INTO agent_sessions
		   (lease_id, agent, external_session_id, started_at, last_seen_at)
		 VALUES ($1, $2, $3, $4, $4)`,
		leaseID, agent, sessionID, now)
	return err
}

// leaseForTest claims a fresh task and returns the resulting lease.
// createTask leaves the task in "ready", so it is claimable as-is.
func leaseForTest(t *testing.T, s *Store, worktree string) *Lease {
	t.Helper()
	task := createTask(t, s, leaseTestNow, defaultTaskInput())
	lease, err := s.Claim(t.Context(), task.ID, "stig", worktree, 0)
	if err != nil {
		t.Fatalf("claim %s: %v", task.ID, err)
	}
	return lease
}

func TestAgentSessionsSchema(t *testing.T) {
	s, _ := openLeaseStore(t)
	lease := leaseForTest(t, s, "host:/wt/one")

	if err := insertRawSession(t, s, lease.ID, "claude-code", "sess-1"); err != nil {
		t.Fatalf("insert valid session: %v", err)
	}

	// cost_currency defaults to USD.
	var currency string
	if err := s.db.QueryRow(
		`SELECT cost_currency FROM agent_sessions WHERE lease_id = $1`, lease.ID,
	).Scan(&currency); err != nil {
		t.Fatalf("read cost_currency: %v", err)
	}
	if currency != "USD" {
		t.Fatalf("cost_currency default: got %q, want %q", currency, "USD")
	}

	// Same (lease, agent, session id) twice is rejected.
	if err := insertRawSession(t, s, lease.ID, "claude-code", "sess-1"); err == nil {
		t.Fatal("duplicate (lease_id, agent, external_session_id) was accepted")
	}

	// The same session id under a DIFFERENT lease is fine: a session survives
	// lease expiry and re-claim.
	other := leaseForTest(t, s, "host:/wt/two")
	if err := insertRawSession(t, s, other.ID, "claude-code", "sess-1"); err != nil {
		t.Fatalf("same session id under a second lease: %v", err)
	}

	// Unknown agent is rejected by the CHECK constraint.
	if err := insertRawSession(t, s, lease.ID, "not-a-tool", "sess-2"); err == nil {
		t.Fatal("unknown agent was accepted")
	}

	// Non-ISO currency is rejected.
	_, err := s.db.Exec(
		`UPDATE agent_sessions SET cost_currency = 'dollars' WHERE lease_id = $1`, lease.ID)
	if err == nil {
		t.Fatal("non-ISO cost_currency was accepted")
	}
	if !strings.Contains(err.Error(), "cost_currency") {
		t.Fatalf("expected a cost_currency constraint error, got: %v", err)
	}
}
