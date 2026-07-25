package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
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

func TestTouchAgentSessionStartsThenHeartbeats(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	sess, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "2.0.1", "sess-1")
	if err != nil {
		t.Fatalf("first touch: %v", err)
	}
	if sess.LeaseID != lease.ID {
		t.Fatalf("lease id: got %d, want %d", sess.LeaseID, lease.ID)
	}
	if sess.AgentVersion != "2.0.1" {
		t.Fatalf("agent version: got %q, want %q", sess.AgentVersion, "2.0.1")
	}
	if !sess.StartedAt.Equal(sess.LastSeenAt) {
		t.Fatalf("first touch should set started_at == last_seen_at, got %v and %v",
			sess.StartedAt, sess.LastSeenAt)
	}
	if sess.EndedAt != nil {
		t.Fatalf("new session should be open, got ended_at %v", *sess.EndedAt)
	}

	// A second touch is a heartbeat: same row, later last_seen_at, unchanged
	// started_at.
	*now = now.Add(5 * time.Minute)
	again, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "2.0.1", "sess-1")
	if err != nil {
		t.Fatalf("second touch: %v", err)
	}
	if again.ID != sess.ID {
		t.Fatalf("heartbeat created a new row: %d then %d", sess.ID, again.ID)
	}
	if !again.StartedAt.Equal(sess.StartedAt) {
		t.Fatalf("heartbeat moved started_at: %v then %v", sess.StartedAt, again.StartedAt)
	}
	if !again.LastSeenAt.After(sess.LastSeenAt) {
		t.Fatalf("heartbeat did not bump last_seen_at: %v then %v", sess.LastSeenAt, again.LastSeenAt)
	}

	// Exactly one row, and exactly one recorded start event.
	var rows, events int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_sessions`).Scan(&rows); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if rows != 1 {
		t.Fatalf("agent_sessions rows: got %d, want 1", rows)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE type = 'agent_session.started'`,
	).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("agent_session.started events: got %d, want 1 (heartbeats must not emit events)", events)
	}
}

func TestTouchAgentSessionRejectsNonHolderAndBadAgent(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	if err := s.CreateActor(ctx, "mallory", "agent", "Mallory", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	_, err := s.TouchAgentSession(ctx, lease.TaskID, "mallory", "claude-code", "", "sess-x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-holder: got %v, want ErrNotFound", err)
	}

	_, err = s.TouchAgentSession(ctx, lease.TaskID, "stig", "not-a-tool", "", "sess-1")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad agent: got %v, want ErrInvalidInput", err)
	}

	_, err = s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty session id: got %v, want ErrInvalidInput", err)
	}
}

func TestTouchAgentSessionEmptyVersionIsNull(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	sess, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1")
	if err != nil {
		t.Fatalf("touch: %v", err)
	}

	var version sql.NullString
	if err := s.db.QueryRow(
		`SELECT agent_version FROM agent_sessions WHERE id = $1`, sess.ID,
	).Scan(&version); err != nil {
		t.Fatalf("read agent_version: %v", err)
	}
	if version.Valid {
		t.Fatalf("agent_version: got %q, want SQL NULL", version.String)
	}
}

func TestAgentSessionNotFound(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	_, err := s.AgentSession(ctx, lease.ID, "claude-code", "no-such-session")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session id: got %v, want ErrNotFound", err)
	}
}

func TestEndAgentSessionStampsEndedAtAndUsage(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	*now = now.Add(time.Hour)
	in, out := int64(1200), int64(340)
	amount := "1.234500"
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{InputTokens: &in, OutputTokens: &out, CostAmount: &amount}); err != nil {
		t.Fatalf("end: %v", err)
	}

	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("ended_at was not stamped")
	}
	if sess.InputTokens == nil || *sess.InputTokens != in {
		t.Fatalf("input_tokens: got %v, want %d", sess.InputTokens, in)
	}
	if sess.OutputTokens == nil || *sess.OutputTokens != out {
		t.Fatalf("output_tokens: got %v, want %d", sess.OutputTokens, out)
	}
	// An amount reported without a currency lands as USD: the column DEFAULT
	// does not fire on the UPDATE path.
	if sess.CostCurrency != "USD" {
		t.Fatalf("cost_currency: got %q, want %q", sess.CostCurrency, "USD")
	}
	if sess.CostAmount == nil || *sess.CostAmount != amount {
		t.Fatalf("cost_amount: got %v, want %s", sess.CostAmount, amount)
	}
}

func TestEndAgentSessionCurrencyAndUnknownSession(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	amount := "2.500000"
	err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{CostAmount: &amount, CostCurrency: "dollars"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad currency: got %v, want ErrInvalidInput", err)
	}

	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{CostAmount: &amount, CostCurrency: "EUR"}); err != nil {
		t.Fatalf("EUR cost: %v", err)
	}
	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if sess.CostCurrency != "EUR" {
		t.Fatalf("cost_currency: got %q, want EUR", sess.CostCurrency)
	}

	err = s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "nope", SessionUsage{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: got %v, want ErrNotFound", err)
	}
}

func TestEndAgentSessionRejectsMalformedCostAmount(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	garbage := "abc"
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{CostAmount: &garbage}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("garbage amount: got %v, want ErrInvalidInput", err)
	}

	overPrecision := "9999999999999"
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{CostAmount: &overPrecision}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-precision amount: got %v, want ErrInvalidInput", err)
	}
}

func TestEndAgentSessionRepeatCloseWithoutReopenIsNotFound(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1", SessionUsage{}); err != nil {
		t.Fatalf("first end: %v", err)
	}

	err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1", SessionUsage{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat close without reopen: got %v, want ErrNotFound", err)
	}
}

func TestEndAgentSessionCurrencyOnlyLeavesCostAmountNil(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{CostCurrency: "EUR"}); err != nil {
		t.Fatalf("currency-only end: %v", err)
	}

	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if sess.CostAmount != nil {
		t.Fatalf("cost_amount: got %v, want nil (no amount was reported)", *sess.CostAmount)
	}
}

func TestEndAgentSessionAfterReopenStoresSecondClose(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	*now = now.Add(time.Hour)
	firstIn := int64(100)
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{InputTokens: &firstIn}); err != nil {
		t.Fatalf("first end: %v", err)
	}

	*now = now.Add(time.Hour)
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("reopen touch: %v", err)
	}

	*now = now.Add(time.Hour)
	secondIn := int64(500)
	amount := "3.000000"
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1",
		SessionUsage{InputTokens: &secondIn, CostAmount: &amount}); err != nil {
		t.Fatalf("second end: %v", err)
	}

	sess, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("ended_at was not stamped on second close")
	}
	if sess.InputTokens == nil || *sess.InputTokens != secondIn {
		t.Fatalf("input_tokens after second close: got %v, want %d", sess.InputTokens, secondIn)
	}
	if sess.CostAmount == nil || *sess.CostAmount != amount {
		t.Fatalf("cost_amount after second close: got %v, want %s", sess.CostAmount, amount)
	}

	var endedEvents int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE type = 'agent_session.ended'`,
	).Scan(&endedEvents); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if endedEvents != 2 {
		t.Fatalf("agent_session.ended events: got %d, want 2 (one per actual close)", endedEvents)
	}
}

func TestTouchAgentSessionReopensClosedSession(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	sess, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1")
	if err != nil {
		t.Fatalf("first touch: %v", err)
	}

	*now = now.Add(time.Hour)
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1", SessionUsage{}); err != nil {
		t.Fatalf("end: %v", err)
	}

	*now = now.Add(time.Hour)
	reopened, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1")
	if err != nil {
		t.Fatalf("touch after end: %v", err)
	}
	if reopened.EndedAt != nil {
		t.Fatalf("reopened session should have ended_at cleared, got %v", *reopened.EndedAt)
	}
	if reopened.ID != sess.ID {
		t.Fatalf("reopen created a new row: %d then %d", sess.ID, reopened.ID)
	}
	if !reopened.StartedAt.Equal(sess.StartedAt) {
		t.Fatalf("reopen moved started_at: %v then %v", sess.StartedAt, reopened.StartedAt)
	}
	if !reopened.LastSeenAt.After(sess.LastSeenAt) {
		t.Fatalf("reopen did not bump last_seen_at: %v then %v", sess.LastSeenAt, reopened.LastSeenAt)
	}

	var events int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE type = 'agent_session.started'`,
	).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("agent_session.started events: got %d, want 1 (reopen must not emit a new start event)", events)
	}
}

// openSessions returns the number of sessions on leaseID with no ended_at.
func openSessions(t *testing.T, s *Store, leaseID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_sessions WHERE lease_id = $1 AND ended_at IS NULL`, leaseID,
	).Scan(&n); err != nil {
		t.Fatalf("count open sessions: %v", err)
	}
	return n
}

func TestReleaseClosesOpenAgentSessions(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got := openSessions(t, s, lease.ID); got != 1 {
		t.Fatalf("open sessions before release: got %d, want 1", got)
	}

	if err := s.Release(ctx, lease.TaskID, "stig"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := openSessions(t, s, lease.ID); got != 0 {
		t.Fatalf("open sessions after release: got %d, want 0", got)
	}
}

func TestExpiryClosesOpenAgentSessions(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got := openSessions(t, s, lease.ID); got != 1 {
		t.Fatalf("open sessions before expiry: got %d, want 1", got)
	}

	*now = now.Add(3 * time.Hour) // past the default 2h TTL
	n, err := s.ExpireLeases(ctx, *now)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired lease count: got %d, want 1", n)
	}
	if got := openSessions(t, s, lease.ID); got != 0 {
		t.Fatalf("open sessions after expiry: got %d, want 0", got)
	}
}

func TestCloseActiveLeaseClosesOpenAgentSessions(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got := openSessions(t, s, lease.ID); got != 1 {
		t.Fatalf("open sessions before CloseActiveLease: got %d, want 1", got)
	}

	_, _, err := s.RecordEvent(ctx, "cli", "close-active-"+lease.TaskID, "task.abandon", nil,
		func(tx *sql.Tx, eventID int64) error {
			return CloseActiveLease(tx, s.Now(), lease.TaskID)
		})
	if err != nil {
		t.Fatalf("CloseActiveLease: %v", err)
	}
	if got := openSessions(t, s, lease.ID); got != 0 {
		t.Fatalf("open sessions after CloseActiveLease: got %d, want 0", got)
	}
}

// TestLeaseCloseKeepsOriginalEndedAt confirms endOpenAgentSessionsOnLease
// only touches still-open sessions: a session already ended keeps the
// ended_at value EndAgentSession stamped, not the later close time.
func TestLeaseCloseKeepsOriginalEndedAt(t *testing.T) {
	s, now := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")
	if _, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := s.EndAgentSession(ctx, lease.TaskID, "stig", "claude-code", "sess-1", SessionUsage{}); err != nil {
		t.Fatalf("end: %v", err)
	}
	ended, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	wantEndedAt := *ended.EndedAt

	*now = now.Add(time.Hour)
	if err := s.Release(ctx, lease.TaskID, "stig"); err != nil {
		t.Fatalf("release: %v", err)
	}

	after, err := s.AgentSession(ctx, lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back after release: %v", err)
	}
	if after.EndedAt == nil || !after.EndedAt.Equal(wantEndedAt) {
		t.Fatalf("ended_at after release: got %v, want unchanged %v", after.EndedAt, wantEndedAt)
	}
}

// TestLeaseCloseLeavesOtherLeaseSessionsOpen confirms endOpenAgentSessionsOnLease
// scopes to the closing lease only: an open session on an unrelated lease
// survives.
func TestLeaseCloseLeavesOtherLeaseSessionsOpen(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	leaseA := leaseForTest(t, s, "host:/wt/one")
	leaseB := leaseForTest(t, s, "host:/wt/two")
	if _, err := s.TouchAgentSession(ctx, leaseA.TaskID, "stig", "claude-code", "", "sess-a"); err != nil {
		t.Fatalf("touch A: %v", err)
	}
	if _, err := s.TouchAgentSession(ctx, leaseB.TaskID, "stig", "claude-code", "", "sess-b"); err != nil {
		t.Fatalf("touch B: %v", err)
	}

	if err := s.Release(ctx, leaseA.TaskID, "stig"); err != nil {
		t.Fatalf("release A: %v", err)
	}

	if got := openSessions(t, s, leaseA.ID); got != 0 {
		t.Fatalf("open sessions on closed lease A: got %d, want 0", got)
	}
	if got := openSessions(t, s, leaseB.ID); got != 1 {
		t.Fatalf("open sessions on untouched lease B: got %d, want 1", got)
	}
}

// TestTouchAgentSessionAfterReleaseIsNotFoundAndCreatesNoRow pins the
// sequential (non-racing) shape of the insert-path guard: once a lease is
// released, an inserting TouchAgentSession call on that task must fail
// closed, not create a session that reads as live on a dead lease.
func TestTouchAgentSessionAfterReleaseIsNotFoundAndCreatesNoRow(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()
	lease := leaseForTest(t, s, "host:/wt/one")

	if err := s.Release(ctx, lease.TaskID, "stig"); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, err := s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch after release: got %v, want ErrNotFound", err)
	}
	if got := openSessions(t, s, lease.ID); got != 0 {
		t.Fatalf("sessions on released lease after touch: got %d, want 0", got)
	}
}

// TestTouchAgentSessionInsertRaceWithLeaseClose races the inserting call
// (first TouchAgentSession for a session) against a concurrent Release on
// the same lease — the interleaving the FOR SHARE lock in
// activeLeaseTxForShare exists to close: without it, a release landing
// between the insert's unlocked lease read and its commit leaves a session
// with ended_at IS NULL on a released lease that nothing will ever close
// again. Whichever side wins the row lock, the invariant must hold: no
// session is left open on a lease that ends up released. The exact
// interleaving is real goroutine/connection-pool scheduling, not something
// this test drives directly, so it runs several iterations to raise the odds
// of exercising both orderings rather than asserting on one.
func TestTouchAgentSessionInsertRaceWithLeaseClose(t *testing.T) {
	s, _ := openLeaseStore(t)
	ctx := t.Context()

	const iterations = 25
	for i := 0; i < iterations; i++ {
		lease := leaseForTest(t, s, fmt.Sprintf("host:/wt/race-%d", i))

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.TouchAgentSession(ctx, lease.TaskID, "stig", "claude-code", "", "sess-1")
		}()
		go func() {
			defer wg.Done()
			_ = s.Release(ctx, lease.TaskID, "stig")
		}()
		wg.Wait()

		if got := openSessions(t, s, lease.ID); got != 0 {
			t.Fatalf("iteration %d: open sessions after racing touch/release: got %d, want 0", i, got)
		}
	}
}
