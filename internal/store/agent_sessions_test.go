package store

import (
	"testing"
)

// insertRawSession inserts directly into agent_sessions, bypassing the store
// API, so schema constraints can be asserted on their own.
func insertRawSession(t *testing.T, s *Store, leaseID int64, agent, sessionID string) error {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT INTO agent_sessions
		   (lease_id, agent, external_session_id, started_at, last_seen_at)
		 VALUES ($1, $2, $3, $4, $4)`,
		leaseID, agent, sessionID, leaseTestNow)
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
	t.Run("cost_currency defaults to USD", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		lease := leaseForTest(t, s, "host:/wt/one")

		if err := insertRawSession(t, s, lease.ID, "claude-code", "sess-1"); err != nil {
			t.Fatalf("insert valid session: %v", err)
		}

		var currency string
		if err := s.db.QueryRow(
			`SELECT cost_currency FROM agent_sessions WHERE lease_id = $1`, lease.ID,
		).Scan(&currency); err != nil {
			t.Fatalf("read cost_currency: %v", err)
		}
		if currency != "USD" {
			t.Fatalf("cost_currency default: got %q, want %q", currency, "USD")
		}
	})

	t.Run("duplicate lease/agent/session id rejected", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		lease := leaseForTest(t, s, "host:/wt/one")

		if err := insertRawSession(t, s, lease.ID, "claude-code", "sess-1"); err != nil {
			t.Fatalf("insert valid session: %v", err)
		}

		err := insertRawSession(t, s, lease.ID, "claude-code", "sess-1")
		if !isUniqueViolationOn(err, "agent_sessions_lease_session_unique") {
			t.Fatalf("duplicate insert: got %v, want a agent_sessions_lease_session_unique violation", err)
		}
	})

	t.Run("same session id under a different lease is fine", func(t *testing.T) {
		// A session survives lease expiry and re-claim, so the same
		// external session id may recur under a fresh lease.
		s, _ := openLeaseStore(t)
		first := leaseForTest(t, s, "host:/wt/one")
		if err := insertRawSession(t, s, first.ID, "claude-code", "sess-1"); err != nil {
			t.Fatalf("insert under first lease: %v", err)
		}

		other := leaseForTest(t, s, "host:/wt/two")
		if err := insertRawSession(t, s, other.ID, "claude-code", "sess-1"); err != nil {
			t.Fatalf("same session id under a second lease: %v", err)
		}
	})

	t.Run("unknown agent rejected", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		lease := leaseForTest(t, s, "host:/wt/one")

		err := insertRawSession(t, s, lease.ID, "not-a-tool", "sess-1")
		if !isCheckViolationOn(err, "agent_sessions_agent_known") {
			t.Fatalf("unknown agent: got %v, want a agent_sessions_agent_known violation", err)
		}
	})

	t.Run("non-ISO cost_currency rejected", func(t *testing.T) {
		s, _ := openLeaseStore(t)
		lease := leaseForTest(t, s, "host:/wt/one")
		if err := insertRawSession(t, s, lease.ID, "claude-code", "sess-1"); err != nil {
			t.Fatalf("insert valid session: %v", err)
		}

		_, err := s.db.Exec(
			`UPDATE agent_sessions SET cost_currency = 'dollars' WHERE lease_id = $1`, lease.ID)
		if !isCheckViolationOn(err, "agent_sessions_cost_currency_format") {
			t.Fatalf("non-ISO cost_currency: got %v, want a agent_sessions_cost_currency_format violation", err)
		}
	})
}
