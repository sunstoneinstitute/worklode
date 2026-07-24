# Agent Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record which coding-agent session is working each leased task, so the backbone can report running sessions and later attribute token cost.

**Architecture:** A new `agent_sessions` child table hangs off `leases` (a lease outlives many sessions). Two store functions — `TouchAgentSession` (start-or-heartbeat, idempotent through a deterministic event external id) and `EndAgentSession` — are exposed as two POST endpoints and driven by `lode hook` events bound to Claude Code's `SessionStart`, `Stop`/`StopFailure`/`SubagentStop`/`Notification`, `PostToolUse(EnterWorktree|ExitWorktree)` and `SessionEnd`.

**Tech Stack:** Go 1.x, Postgres (database/sql + pgx stdlib), golang-migrate, cobra, net/http `ServeMux`.

**Spec:** `docs/specs/2026-07-25-agent-sessions-design.md`

**Prerequisite:** store tests need Postgres. Run `docker compose up -d postgres` once before starting; tests skip (not fail) if it is unreachable and `CI` is unset.

---

### Task 1: Migration 0003 — the `agent_sessions` table

**Files:**
- Create: `deploy/base/migrations/0003_agent_sessions.up.sql`
- Create: `deploy/base/migrations/0003_agent_sessions.down.sql`
- Modify: `deploy/base/kustomization.yaml:12-18` (configMapGenerator file list)
- Test: `internal/store/agent_sessions_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/agent_sessions_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestAgentSessionsSchema -v`
Expected: FAIL with `relation "agent_sessions" does not exist`.

- [ ] **Step 3: Write the migration**

Create `deploy/base/migrations/0003_agent_sessions.up.sql`:

```sql
CREATE TABLE agent_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lease_id            bigint NOT NULL REFERENCES leases(id) ON DELETE RESTRICT,
    agent               text NOT NULL CHECK (agent IN
                          ('claude-code','codex','cursor','aider',
                           'opencode','pi','amp','other')),
    agent_version       text,
    external_session_id text NOT NULL,
    started_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    ended_at            timestamptz,
    input_tokens        bigint,
    output_tokens       bigint,
    cost_amount         numeric(12,6),
    cost_currency       text NOT NULL DEFAULT 'USD'
                          CHECK (cost_currency ~ '^[A-Z]{3}$'),
    UNIQUE (lease_id, agent, external_session_id)
);
CREATE INDEX agent_sessions_lease ON agent_sessions (lease_id);
```

Create `deploy/base/migrations/0003_agent_sessions.down.sql`:

```sql
DROP TABLE agent_sessions;
```

- [ ] **Step 4: Register the migration with kustomize**

In `deploy/base/kustomization.yaml`, add two lines to the `worklode-migrations` file list, after `migrations/0002_prioritization.down.sql`:

```yaml
      - migrations/0003_agent_sessions.up.sql
      - migrations/0003_agent_sessions.down.sql
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestAgentSessionsSchema -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0003_agent_sessions.up.sql \
        deploy/base/migrations/0003_agent_sessions.down.sql \
        deploy/base/kustomization.yaml \
        internal/store/agent_sessions_test.go
git commit -m "Add agent_sessions table"
```

---

### Task 2: `TouchAgentSession` — start or heartbeat

**Files:**
- Create: `internal/store/agent_sessions.go`
- Test: `internal/store/agent_sessions_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/agent_sessions_test.go`:

```go
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
```

`errors` is already imported by the test file from Task 1 only if you added it; add `"errors"` to that file's import block now.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestTouchAgentSession -v`
Expected: FAIL — `s.TouchAgentSession undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/agent_sessions.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// AgentSession is one coding-agent session's work on one lease. A lease
// outlives many sessions (restarts, /clear, next-day resumption), and a single
// session can span several leases when it moves between worktrees — so the
// natural key is (lease_id, agent, external_session_id), not the lease alone.
type AgentSession struct {
	ID           int64
	LeaseID      int64
	Agent        string
	AgentVersion string
	SessionID    string
	StartedAt    time.Time
	LastSeenAt   time.Time
	EndedAt      *time.Time
	InputTokens  *int64
	OutputTokens *int64
	CostAmount   *string
	CostCurrency string
}

// validAgents mirrors the agent_sessions.agent CHECK constraint in Go so
// callers get ErrInvalidInput instead of a raw constraint violation.
var validAgents = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"cursor":      true,
	"aider":       true,
	"opencode":    true,
	"pi":          true,
	"amp":         true,
	"other":       true,
}

// currencyRE mirrors the cost_currency CHECK constraint (ISO 4217).
var currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)

// defaultCurrency is applied when a caller reports a cost amount without a
// currency. The column DEFAULT cannot do this: cost arrives by UPDATE, and a
// column default only fires on INSERT.
const defaultCurrency = "USD"

const agentSessionColumns = `id, lease_id, agent, agent_version, external_session_id,
	started_at, last_seen_at, ended_at, input_tokens, output_tokens,
	cost_amount, cost_currency`

func scanAgentSession(row rowScanner) (*AgentSession, error) {
	var a AgentSession
	var version, costAmount sql.NullString
	var endedAt sql.NullTime
	var inTok, outTok sql.NullInt64
	if err := row.Scan(&a.ID, &a.LeaseID, &a.Agent, &version, &a.SessionID,
		&a.StartedAt, &a.LastSeenAt, &endedAt, &inTok, &outTok,
		&costAmount, &a.CostCurrency); err != nil {
		return nil, err
	}
	a.AgentVersion = version.String
	a.StartedAt = a.StartedAt.UTC()
	a.LastSeenAt = a.LastSeenAt.UTC()
	if endedAt.Valid {
		t := endedAt.Time.UTC()
		a.EndedAt = &t
	}
	if inTok.Valid {
		a.InputTokens = &inTok.Int64
	}
	if outTok.Valid {
		a.OutputTokens = &outTok.Int64
	}
	if costAmount.Valid {
		a.CostAmount = &costAmount.String
	}
	return &a, nil
}

// nullIfEmpty maps "" to a SQL NULL, so optional text columns stay NULL
// rather than holding an empty string.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// heldLease returns the active lease on taskID, erroring with ErrNotFound
// unless actorID holds it. A non-holder and an absent lease are deliberately
// indistinguishable, the same probe-resistant policy as Renew and Release.
func (s *Store) heldLease(ctx context.Context, taskID, actorID string) (*Lease, error) {
	l, err := s.ActiveLease(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if l.ActorID != actorID {
		return nil, fmt.Errorf("no active lease on task %s held by %s: %w", taskID, actorID, ErrNotFound)
	}
	return l, nil
}

// TouchAgentSession records that agent/sessionID is working taskID, and is
// both "start" and "heartbeat": the first call inserts the row inside a
// recorded "agent_session.started" event, every call bumps last_seen_at.
//
// The event's external id is deterministic, so repeat calls take RecordEvent's
// already-recorded path and emit no further events. That also means the
// last_seen_at bump must happen OUTSIDE the event — apply is skipped entirely
// on the repeat path.
//
// Errors: ErrInvalidInput for an unknown agent or empty session id,
// ErrNotFound when actorID does not hold an active lease on taskID.
func (s *Store) TouchAgentSession(ctx context.Context, taskID, actorID, agent, agentVersion, sessionID string) (*AgentSession, error) {
	if !validAgents[agent] {
		return nil, fmt.Errorf("unknown agent %q: %w", agent, ErrInvalidInput)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required: %w", ErrInvalidInput)
	}

	lease, err := s.heldLease(ctx, taskID, actorID)
	if err != nil {
		return nil, err
	}
	now := s.nowFn().UTC().Truncate(time.Second)
	extID := fmt.Sprintf("agent-session-%d-%s-%s", lease.ID, agent, sessionID)

	_, _, err = s.RecordEvent(ctx, "cli", extID, "agent_session.started", nil,
		func(tx *sql.Tx, eventID int64) error {
			// Re-check inside the tx: the lease may have been released and
			// re-claimed since heldLease read it, which would make extID refer
			// to a different lease than the one being written.
			cur, err := activeLeaseTx(tx, taskID)
			if err != nil {
				return err
			}
			if cur.ID != lease.ID || cur.ActorID != actorID {
				return fmt.Errorf("no active lease on task %s held by %s: %w", taskID, actorID, ErrNotFound)
			}
			if _, err := tx.Exec(
				`INSERT INTO agent_sessions
				   (lease_id, agent, agent_version, external_session_id, started_at, last_seen_at)
				 VALUES ($1, $2, $3, $4, $5, $5)
				 ON CONFLICT (lease_id, agent, external_session_id) DO NOTHING`,
				lease.ID, agent, nullIfEmpty(agentVersion), sessionID, now,
			); err != nil {
				return fmt.Errorf("insert agent session on lease %d: %w", lease.ID, err)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE agent_sessions SET last_seen_at = $1
		  WHERE lease_id = $2 AND agent = $3 AND external_session_id = $4
		    AND ended_at IS NULL`,
		now, lease.ID, agent, sessionID,
	); err != nil {
		return nil, fmt.Errorf("bump agent session last_seen_at: %w", err)
	}

	return s.AgentSession(ctx, lease.ID, agent, sessionID)
}

// AgentSession returns one session row by its natural key, or ErrNotFound.
func (s *Store) AgentSession(ctx context.Context, leaseID int64, agent, sessionID string) (*AgentSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+agentSessionColumns+` FROM agent_sessions
		  WHERE lease_id = $1 AND agent = $2 AND external_session_id = $3`,
		leaseID, agent, sessionID)
	a, err := scanAgentSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("agent session %s/%s on lease %d: %w", agent, sessionID, leaseID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get agent session %s/%s: %w", agent, sessionID, err)
	}
	return a, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestTouchAgentSession -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/store/agent_sessions.go internal/store/agent_sessions_test.go
git commit -m "Add TouchAgentSession store function"
```

---

### Task 3: `EndAgentSession` — close a session, record usage

**Files:**
- Modify: `internal/store/agent_sessions.go` (append)
- Test: `internal/store/agent_sessions_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/agent_sessions_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestEndAgentSession -v`
Expected: FAIL — `undefined: SessionUsage`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/agent_sessions.go`:

```go
// SessionUsage carries the optional accounting a caller can report when
// ending a session. A nil field leaves the stored value untouched.
// CostAmount is a decimal string (not float64) so numeric(12,6) round-trips
// exactly. CostCurrency "" means defaultCurrency.
type SessionUsage struct {
	InputTokens  *int64
	OutputTokens *int64
	CostAmount   *string
	CostCurrency string
}

// EndAgentSession closes the open session agent/sessionID on taskID's active
// lease, stamping ended_at and writing whatever usage the caller supplied.
// Only the lease holder may end a session (ErrNotFound otherwise); an unknown
// or already-closed session is ErrNotFound. A cost amount without a currency
// is stored as USD.
func (s *Store) EndAgentSession(ctx context.Context, taskID, actorID, agent, sessionID string, usage SessionUsage) error {
	if !validAgents[agent] {
		return fmt.Errorf("unknown agent %q: %w", agent, ErrInvalidInput)
	}
	if sessionID == "" {
		return fmt.Errorf("session id is required: %w", ErrInvalidInput)
	}
	currency := usage.CostCurrency
	if currency == "" {
		currency = defaultCurrency
	}
	if !currencyRE.MatchString(currency) {
		return fmt.Errorf("cost currency %q is not an ISO 4217 code: %w", usage.CostCurrency, ErrInvalidInput)
	}

	lease, err := s.heldLease(ctx, taskID, actorID)
	if err != nil {
		return err
	}
	now := s.nowFn().UTC().Truncate(time.Second)
	extID := fmt.Sprintf("agent-session-ended-%d-%s-%s", lease.ID, agent, sessionID)

	_, _, err = s.RecordEvent(ctx, "cli", extID, "agent_session.ended", nil,
		func(tx *sql.Tx, eventID int64) error {
			res, err := tx.Exec(
				`UPDATE agent_sessions
				    SET ended_at      = $1,
				        input_tokens  = COALESCE($2, input_tokens),
				        output_tokens = COALESCE($3, output_tokens),
				        cost_amount   = COALESCE($4, cost_amount),
				        cost_currency = CASE WHEN $4 IS NULL THEN cost_currency ELSE $5 END
				  WHERE lease_id = $6 AND agent = $7 AND external_session_id = $8
				    AND ended_at IS NULL`,
				now, usage.InputTokens, usage.OutputTokens, usage.CostAmount, currency,
				lease.ID, agent, sessionID)
			if err != nil {
				return fmt.Errorf("end agent session %s/%s: %w", agent, sessionID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("end agent session %s/%s: %w", agent, sessionID, err)
			}
			if n == 0 {
				return fmt.Errorf("no open agent session %s/%s on task %s: %w",
					agent, sessionID, taskID, ErrNotFound)
			}
			return nil
		})
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestEndAgentSession -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/store/agent_sessions.go internal/store/agent_sessions_test.go
git commit -m "Add EndAgentSession store function"
```

---

### Task 4: Closing a lease closes its open sessions

**Files:**
- Modify: `internal/store/agent_sessions.go` (append helper)
- Modify: `internal/store/leases.go:304-333` (`closeLease` and `CloseActiveLease`)
- Test: `internal/store/agent_sessions_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/agent_sessions_test.go`:

```go
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

	*now = now.Add(3 * time.Hour) // past the default 2h TTL
	if _, err := s.ExpireLeases(ctx, *now); err != nil {
		t.Fatalf("expire: %v", err)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestReleaseClosesOpen|TestExpiryClosesOpen|TestCloseActiveLeaseClosesOpen' -v`
Expected: FAIL — each reports `open sessions after ...: got 1, want 0`.

- [ ] **Step 3: Add the helper**

Append to `internal/store/agent_sessions.go`:

```go
// endOpenAgentSessionsOnLease stamps ended_at on every still-open session for
// leaseID. Called whenever a lease closes: a released, swept, or completed
// task must never leave a session that reads as live.
func endOpenAgentSessionsOnLease(tx *sql.Tx, now time.Time, leaseID int64) error {
	if _, err := tx.Exec(
		`UPDATE agent_sessions SET ended_at = $1 WHERE lease_id = $2 AND ended_at IS NULL`,
		now.UTC(), leaseID,
	); err != nil {
		return fmt.Errorf("end open agent sessions on lease %d: %w", leaseID, err)
	}
	return nil
}

// endOpenAgentSessionsOnTask is endOpenAgentSessionsOnLease for the active
// lease on taskID. It MUST run before the lease's released_at is set — once
// that is written, the subquery no longer matches.
func endOpenAgentSessionsOnTask(tx *sql.Tx, now time.Time, taskID string) error {
	if _, err := tx.Exec(
		`UPDATE agent_sessions SET ended_at = $1
		  WHERE ended_at IS NULL
		    AND lease_id IN (SELECT id FROM leases WHERE task_id = $2 AND released_at IS NULL)`,
		now.UTC(), taskID,
	); err != nil {
		return fmt.Errorf("end open agent sessions on task %s: %w", taskID, err)
	}
	return nil
}
```

- [ ] **Step 4: Call it from both lease-closing paths**

In `internal/store/leases.go`, in `closeLease`, add the call immediately after the `UPDATE leases SET released_at` block and before the task-state read:

```go
	if err := endOpenAgentSessionsOnLease(tx, now, leaseID); err != nil {
		return err
	}
```

In `CloseActiveLease`, add the call **before** the `UPDATE leases` statement:

```go
func CloseActiveLease(tx *sql.Tx, now time.Time, taskID string) error {
	// Before released_at is written: the lookup below matches only unreleased
	// leases.
	if err := endOpenAgentSessionsOnTask(tx, now, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE leases SET released_at = $1 WHERE task_id = $2 AND released_at IS NULL`,
		now.UTC(), taskID,
	); err != nil {
		return fmt.Errorf("close active lease on %s: %w", taskID, err)
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS — all store tests, including the pre-existing lease tests.

- [ ] **Step 6: Commit**

```bash
git add internal/store/agent_sessions.go internal/store/agent_sessions_test.go internal/store/leases.go
git commit -m "End open agent sessions when a lease closes"
```

---

### Task 5: HTTP endpoints

**Files:**
- Create: `internal/api/agentsessions.go`
- Modify: `internal/api/server.go:217-221` (route table)
- Test: `internal/api/agentsessions_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/agentsessions_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"
)

// claimedTask creates a ready task, claims it as alice, and returns its id.
// Mirrors the setup in lifecycle_test.go — reuse that file's helper if one
// already exists rather than duplicating it.
func claimedTask(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	id := readyTask(t, h, token)
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token,
		map[string]any{"worktree": "host:/wt/one"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim: got %d, body %s", rr.Code, rr.Body.String())
	}
	return id
}

func TestAgentSessionEndpoints(t *testing.T) {
	_, h, token := newTestServer(t)
	id := claimedTask(t, h, token)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "agent_version": "2.0.1", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("touch: got %d, body %s", rr.Code, rr.Body.String())
	}

	var got struct {
		Agent      string `json:"agent"`
		SessionID  string `json:"session_id"`
		LastSeenAt string `json:"last_seen_at"`
		EndedAt    string `json:"ended_at"`
	}
	decodeJSON(t, rr, &got)
	if got.Agent != "claude-code" || got.SessionID != "sess-1" {
		t.Fatalf("touch body: %+v", got)
	}
	if got.LastSeenAt == "" {
		t.Fatal("touch body: last_seen_at missing")
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1", "input_tokens": 42})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("end: got %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestAgentSessionRejectsNonHolderAndBadAgent(t *testing.T) {
	st, h, token := newTestServer(t)
	id := claimedTask(t, h, token)

	other := secondActor(t, st, "bob")
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", other,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("non-holder: got %d, want 404, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "not-a-tool", "session_id": "sess-1"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad agent: got %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing session id: got %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}
```

Before running: `readyTask` and `decodeJSON` are assumed to exist in the `api_test` package. Check `internal/api/server_test.go` and `internal/api/tasks_test.go` for the actual helper names (search for `func readyTask` and `func decodeJSON`). If a helper is missing, inline its two or three lines rather than inventing a name — e.g. create the task with `doReq(t, h, "POST", "/api/v1/tasks", token, …)` and move it to `ready` the same way `lifecycle_test.go` does, and decode with `json.Unmarshal(rr.Body.Bytes(), &got)`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestAgentSession -v`
Expected: FAIL — 404 from the mux, because the routes do not exist.

- [ ] **Step 3: Write the handlers**

Create `internal/api/agentsessions.go`:

```go
package api

import (
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// agentSessionJSON is the wire form of an agent session.
type agentSessionJSON struct {
	LeaseID      int64      `json:"lease_id"`
	Agent        string     `json:"agent"`
	AgentVersion string     `json:"agent_version,omitempty"`
	SessionID    string     `json:"session_id"`
	StartedAt    time.Time  `json:"started_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
}

func toAgentSessionJSON(a *store.AgentSession) agentSessionJSON {
	return agentSessionJSON{
		LeaseID:      a.LeaseID,
		Agent:        a.Agent,
		AgentVersion: a.AgentVersion,
		SessionID:    a.SessionID,
		StartedAt:    a.StartedAt,
		LastSeenAt:   a.LastSeenAt,
		EndedAt:      a.EndedAt,
	}
}

type agentSessionRequest struct {
	Agent        string `json:"agent"`
	AgentVersion string `json:"agent_version"`
	SessionID    string `json:"session_id"`
}

// touchAgentSession handles POST /api/v1/tasks/{id}/agent-session: record
// that an agent session is working the task, or heartbeat an existing one.
// Only the lease holder may report; a non-holder gets 404, the same
// probe-resistant answer as renew.
func (s *server) touchAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentSessionRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)

	sess, err := s.st.TouchAgentSession(r.Context(), id, actor.ID,
		req.Agent, req.AgentVersion, req.SessionID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAgentSessionJSON(sess))
}

type agentSessionEndRequest struct {
	Agent        string  `json:"agent"`
	SessionID    string  `json:"session_id"`
	InputTokens  *int64  `json:"input_tokens"`
	OutputTokens *int64  `json:"output_tokens"`
	CostAmount   *string `json:"cost_amount"`
	CostCurrency string  `json:"cost_currency"`
}

// endAgentSession handles POST /api/v1/tasks/{id}/agent-session/end.
func (s *server) endAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req agentSessionEndRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)

	err := s.st.EndAgentSession(r.Context(), id, actor.ID, req.Agent, req.SessionID,
		store.SessionUsage{
			InputTokens:  req.InputTokens,
			OutputTokens: req.OutputTokens,
			CostAmount:   req.CostAmount,
			CostCurrency: req.CostCurrency,
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, directly after the `lease/worktree` route (line ~221):

```go
	mux.Handle("POST /api/v1/tasks/{id}/agent-session", s.auth(s.touchAgentSession))
	mux.Handle("POST /api/v1/tasks/{id}/agent-session/end", s.auth(s.endAgentSession))
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestAgentSession -v`
Expected: PASS (both tests)

- [ ] **Step 6: Commit**

```bash
git add internal/api/agentsessions.go internal/api/agentsessions_test.go internal/api/server.go
git commit -m "Add agent-session HTTP endpoints"
```

---

### Task 6: CLI client methods

**Files:**
- Modify: `internal/cli/client.go` (append after `RebindWorktree`, ~line 502)
- Test: `internal/cli/client_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client_test.go`. Match the existing style in that file for standing up a stub server — check how `TestClientBriefAndRebindWorktree` (line ~369) does it and reuse the same helper:

```go
func TestClientAgentSession(t *testing.T) {
	var gotPaths []string
	var gotBodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		gotBodies = append(gotBodies, string(body))
		if strings.HasSuffix(r.URL.Path, "/end") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lease_id":7,"agent":"claude-code","session_id":"sess-1",
			"started_at":"2026-07-19T12:00:00Z","last_seen_at":"2026-07-19T12:05:00Z"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts.URL)

	sess, _, err := c.TouchAgentSession(context.Background(), "WL-1", "claude-code", "2.0.1", "sess-1")
	if err != nil {
		t.Fatalf("TouchAgentSession: %v", err)
	}
	if sess.SessionID != "sess-1" || sess.LeaseID != 7 {
		t.Fatalf("decoded session: %+v", sess)
	}
	if gotPaths[0] != "POST /api/v1/tasks/WL-1/agent-session" {
		t.Fatalf("path: %s", gotPaths[0])
	}
	if !strings.Contains(gotBodies[0], `"agent_version":"2.0.1"`) {
		t.Fatalf("body: %s", gotBodies[0])
	}

	if err := c.EndAgentSession(context.Background(), "WL-1",
		EndAgentSessionInput{Agent: "claude-code", SessionID: "sess-1"}); err != nil {
		t.Fatalf("EndAgentSession: %v", err)
	}
	if gotPaths[1] != "POST /api/v1/tasks/WL-1/agent-session/end" {
		t.Fatalf("path: %s", gotPaths[1])
	}
}
```

`newTestClient` is the assumed local helper for building a `*Client` against a URL — use whatever `client_test.go` already uses (search for `func newTestClient` or how existing tests construct the client) rather than adding a second one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestClientAgentSession -v`
Expected: FAIL — `c.TouchAgentSession undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/cli/client.go`, after `RebindWorktree`:

```go
// AgentSession is the wire form of an agent session on a task's lease.
type AgentSession struct {
	LeaseID      int64      `json:"lease_id"`
	Agent        string     `json:"agent"`
	AgentVersion string     `json:"agent_version,omitempty"`
	SessionID    string     `json:"session_id"`
	StartedAt    time.Time  `json:"started_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
}

// TouchAgentSession calls POST /api/v1/tasks/{id}/agent-session: report that
// this agent session is working id, or heartbeat an already-reported one.
func (c *Client) TouchAgentSession(ctx context.Context, id, agent, agentVersion, sessionID string) (AgentSession, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session",
		map[string]string{
			"agent":         agent,
			"agent_version": agentVersion,
			"session_id":    sessionID,
		})
	if err != nil {
		return AgentSession{}, nil, err
	}
	var a AgentSession
	if err := json.Unmarshal(raw, &a); err != nil {
		return AgentSession{}, nil, fmt.Errorf("decode agent session: %w", err)
	}
	return a, raw, nil
}

// EndAgentSessionInput carries the required identity plus optional accounting
// for ending a session. A nil usage field leaves the stored value untouched.
type EndAgentSessionInput struct {
	Agent        string
	SessionID    string
	InputTokens  *int64
	OutputTokens *int64
	CostAmount   *string
	CostCurrency string
}

// EndAgentSession calls POST /api/v1/tasks/{id}/agent-session/end.
func (c *Client) EndAgentSession(ctx context.Context, id string, in EndAgentSessionInput) error {
	body := map[string]any{"agent": in.Agent, "session_id": in.SessionID}
	if in.InputTokens != nil {
		body["input_tokens"] = *in.InputTokens
	}
	if in.OutputTokens != nil {
		body["output_tokens"] = *in.OutputTokens
	}
	if in.CostAmount != nil {
		body["cost_amount"] = *in.CostAmount
	}
	if in.CostCurrency != "" {
		body["cost_currency"] = in.CostCurrency
	}
	_, err := c.do(ctx, http.MethodPost,
		"/api/v1/tasks/"+url.PathEscape(id)+"/agent-session/end", body)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -run TestClientAgentSession -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "Add agent-session client methods"
```

---

### Task 7: Session marker gains a heartbeat timestamp

The marker file already records the live session per worktree. It gains
`last_heartbeat_at` so heartbeats can be debounced client-side, and so the git
`pre-commit` hook (which gets no stdin) can find the session id.

**Files:**
- Modify: `internal/hookrun/hookrun.go:459-500` (marker helpers)
- Test: `internal/hookrun/hookrun_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/hookrun/hookrun_test.go`:

```go
func TestSessionMarkerHeartbeat(t *testing.T) {
	root := initGitRepo(t)
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	if err := writeSessionMarker(root, "sess-1", base); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	id, ok := markerSessionID(root)
	if !ok || id != "sess-1" {
		t.Fatalf("markerSessionID: got %q, %v", id, ok)
	}

	// Within the debounce window: no heartbeat is due.
	if heartbeatDue(root, base.Add(30*time.Second)) {
		t.Fatal("heartbeat due 30s after the last one; want debounced")
	}
	// Past the window: due again.
	if !heartbeatDue(root, base.Add(2*time.Minute)) {
		t.Fatal("heartbeat not due 2m after the last one")
	}

	// Recording a heartbeat moves the window without disturbing the session id.
	if err := recordHeartbeat(root, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if heartbeatDue(root, base.Add(2*time.Minute+30*time.Second)) {
		t.Fatal("heartbeat due 30s after a recorded heartbeat; want debounced")
	}
	if id, ok := markerSessionID(root); !ok || id != "sess-1" {
		t.Fatalf("session id after heartbeat: got %q, %v", id, ok)
	}

	// No marker at all: nothing to heartbeat, and no session id.
	empty := initGitRepo(t)
	if heartbeatDue(empty, base) {
		t.Fatal("heartbeat due with no marker file")
	}
	if _, ok := markerSessionID(empty); ok {
		t.Fatal("markerSessionID found an id with no marker file")
	}
}
```

`initGitRepo` is the existing helper in `hookrun_test.go` (~line 330): it makes
a temp dir a git repo with one commit and returns the path resolved to git's
own toplevel. The marker lives in the worktree-private git dir, so
`worktree.GitDir` must resolve — a bare `t.TempDir()` will not do.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hookrun/ -run TestSessionMarkerHeartbeat -v`
Expected: FAIL — `too many arguments in call to writeSessionMarker`, `undefined: heartbeatDue`.

- [ ] **Step 3: Write the implementation**

In `internal/hookrun/hookrun.go`, add the debounce constant next to `leaseRenewWindow`:

```go
// heartbeatDebounce is the minimum gap between two agent-session heartbeats
// reported from one worktree. Stop fires per assistant turn, which in a fast
// conversation is several times a minute; the backbone does not need that.
const heartbeatDebounce = time.Minute
```

Replace the marker struct and helpers:

```go
// sessionMarker records the process owning a live coding session in a
// worktree. A marker is stale once its pid is no longer alive.
type sessionMarker struct {
	SessionID       string `json:"session_id"`
	PID             int    `json:"pid"`
	StartedAt       string `json:"started_at"`
	LastHeartbeatAt string `json:"last_heartbeat_at,omitempty"`
}

// readSessionMarker reads root's marker. A missing or unparseable marker
// returns ok=false — never an error, since every caller treats "no marker" as
// "nothing to do".
func readSessionMarker(root string) (sessionMarker, bool) {
	path, err := markerPath(root)
	if err != nil {
		return sessionMarker{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionMarker{}, false
	}
	var m sessionMarker
	if json.Unmarshal(data, &m) != nil {
		return sessionMarker{}, false
	}
	return m, true
}

// writeMarker serializes m to root's marker path.
func writeMarker(root string, m sessionMarker) error {
	path, err := markerPath(root)
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// writeSessionMarker writes the current process's session marker for root,
// counting now as the first heartbeat.
func writeSessionMarker(root, sessionID string, now time.Time) error {
	return writeMarker(root, sessionMarker{
		SessionID:       sessionID,
		PID:             os.Getpid(),
		StartedAt:       now.Format(time.RFC3339),
		LastHeartbeatAt: now.Format(time.RFC3339),
	})
}

// markerSessionID returns the session id recorded for root. Used by hooks that
// receive no stdin (git pre-commit) and so cannot learn it from a payload.
func markerSessionID(root string) (string, bool) {
	m, ok := readSessionMarker(root)
	if !ok || m.SessionID == "" {
		return "", false
	}
	return m.SessionID, true
}

// heartbeatDue reports whether root's session is due another heartbeat. No
// marker means no session to report, so nothing is due. An unparseable
// timestamp counts as due — reporting once too often beats going silent.
func heartbeatDue(root string, now time.Time) bool {
	m, ok := readSessionMarker(root)
	if !ok || m.SessionID == "" {
		return false
	}
	last, err := time.Parse(time.RFC3339, m.LastHeartbeatAt)
	if err != nil {
		return true
	}
	return now.Sub(last) >= heartbeatDebounce
}

// recordHeartbeat stamps root's marker with the time of a heartbeat that was
// just reported to the backbone.
func recordHeartbeat(root string, now time.Time) error {
	m, ok := readSessionMarker(root)
	if !ok {
		return nil
	}
	m.LastHeartbeatAt = now.Format(time.RFC3339)
	return writeMarker(root, m)
}
```

Then rewrite `sessionMarkerFresh` to use the shared reader. Behavior is
unchanged — it just stops duplicating the read/unmarshal:

```go
// sessionMarkerFresh reports whether root has a session marker whose recorded
// pid is still alive. An absent/unreadable marker, or a dead pid, is stale.
func sessionMarkerFresh(root string) bool {
	m, ok := readSessionMarker(root)
	if !ok || m.PID <= 0 {
		return false
	}
	return pidAlive(m.PID)
}
```

`pidAlive` already exists directly below it and is unchanged.

- [ ] **Step 4: Fix the existing caller**

`handleSessionStart` calls `writeSessionMarker(root, p.SessionID)`. Update it to pass the clock:

```go
	if err := writeSessionMarker(root, p.SessionID, opts.now()); err != nil {
		warn(opts, "write session marker: %v", err)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/hookrun/ -v`
Expected: PASS — the new test and every pre-existing hookrun test.

- [ ] **Step 6: Commit**

```bash
git add internal/hookrun/hookrun.go internal/hookrun/hookrun_test.go
git commit -m "Add heartbeat timestamp to the session marker"
```

---

### Task 8: Hook events — `heartbeat`, `worktree-enter`, `worktree-exit`, and reporting from session start/end

**Files:**
- Modify: `internal/hookrun/hookrun.go` (guarded event set, `dispatch`, handlers)
- Modify: `internal/cmd/hook.go:23-28` (`Use`/`Short` text)
- Test: `internal/hookrun/hookrun_test.go` (append; update `allEvents`)

- [ ] **Step 1: Write the failing test**

In `internal/hookrun/hookrun_test.go`, extend the guarded event list:

```go
var allEvents = []string{"session-start", "session-end", "pre-commit",
	"worktree-create", "worktree-remove", "heartbeat", "worktree-enter", "worktree-exit"}
```

Then append:

```go
// runHook drives one hook invocation the way the existing tests do inline,
// and fails the test on a non-zero exit code.
func runHook(t *testing.T, event string, p Payload) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  event,
		Stdin:  bytes.NewReader(payloadJSON(t, p)),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("%s exit code = %d, want 0 (stderr: %s)", event, code, stderr.String())
	}
}

func TestHeartbeatReportsAgentSession(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Heartbeat task")

	// session-start opens the session and writes the marker.
	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if !rec.hitAny("/agent-session") {
		t.Fatal("session-start did not report the agent session")
	}

	// A heartbeat inside the debounce window makes no backbone call.
	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if rec.count("/agent-session") != before {
		t.Fatal("heartbeat inside the debounce window still called the backbone")
	}

	// The session is recorded against the task's lease.
	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	if sess.EndedAt != nil {
		t.Fatal("session should still be open")
	}

	// session-end closes it.
	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1"})
	sess, err = st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session after end: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("session-end did not close the session")
	}
}

func TestHeartbeatOutsideWorktreeIsNOP(t *testing.T) {
	rec := newRecordingServer(t)
	runHook(t, "heartbeat", Payload{Cwd: t.TempDir(), SessionID: "sess-1"})
	if rec.hit() {
		t.Fatal("heartbeat outside a Worklode worktree called the backbone")
	}
}
```

`newRealServer`, `initGitRepo`, `setupLeasedWorktree` and `payloadJSON` all
already exist in `hookrun_test.go`. `runHook` is defined in the snippet above
(the existing tests build `Options` inline; this factors that out). One helper
must be added — `rec.count(substr)`, a counting sibling of `hitAny`, next to it:

```go
func (p *pathRecorder) count(substr string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.paths {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hookrun/ -run 'TestHeartbeat' -v`
Expected: FAIL — `session-start did not report the agent session` (and unknown-event warnings for `heartbeat`).

- [ ] **Step 3: Add the agent-identity helper and the report calls**

In `internal/hookrun/hookrun.go`, add near the top-level helpers:

```go
// agentName is the coding agent reporting this hook. Claude Code is the only
// integration today, so it is the default; other agents set LODE_AGENT. It is
// an env var rather than a flag because `lode hook` disables flag parsing so
// the --next argv passes through verbatim.
func agentName() string {
	if a := os.Getenv("LODE_AGENT"); a != "" {
		return a
	}
	return "claude-code"
}

// reportSession reports an agent session on taskID and stamps the marker's
// heartbeat time. Like every hookrun backbone call it is bounded and
// downgrades failure to a warning.
func reportSession(ctx context.Context, opts Options, c *cli.Client, taskID, root, sessionID string) {
	if sessionID == "" {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if _, _, err := c.TouchAgentSession(sctx, taskID, agentName(), "", sessionID); err != nil {
		warn(opts, "report agent session on %s: %v", taskID, err)
		return
	}
	if err := recordHeartbeat(root, opts.now()); err != nil {
		warn(opts, "record heartbeat: %v", err)
	}
}
```

In `handleSessionStart`, report the session immediately after `ensureLease` and after the marker is written (the marker must exist before `recordHeartbeat` can stamp it):

```go
	ensureLease(ctx, opts, c, taskID, identity, brief.Lease)

	if err := writeSessionMarker(root, p.SessionID, opts.now()); err != nil {
		warn(opts, "write session marker: %v", err)
	}
	reportSession(ctx, opts, c, taskID, root, p.SessionID)

	emitAdditionalContext(opts.Stdout, compactBrief(brief))
```

- [ ] **Step 4: Add the three new handlers**

Append to the handler section of `internal/hookrun/hookrun.go`:

```go
// handleHeartbeat reports that this worktree's session is still alive. Bound
// to Stop, StopFailure, SubagentStop and Notification: between them they cover
// a session that finishes a turn, dies on an API error, spends a long turn in
// subagents, or sits blocked on a human. Debounced against the marker so a
// fast conversation does not flood the backbone.
func handleHeartbeat(ctx context.Context, opts Options, p Payload, dir string) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	taskID, ok := worktree.ParseDir(root)
	if !ok {
		return
	}
	if !heartbeatDue(root, opts.now()) {
		return
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID, _ = markerSessionID(root)
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	reportSession(ctx, opts, c, taskID, root, sessionID)
}

// handleWorktreeEnter reports the session against the lease of the worktree it
// just moved into. One session can work several tasks in sequence; each gets
// its own row, keyed by (lease, agent, session id).
func handleWorktreeEnter(ctx context.Context, opts Options, p Payload, dir string) {
	entered := payloadPath(p, dir)
	root, ok := worktree.Root(entered)
	if !ok {
		return
	}
	taskID, ok := worktree.ParseDir(root)
	if !ok {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	reportSession(ctx, opts, c, taskID, root, p.SessionID)
}

// handleWorktreeExit closes the session's row on the worktree it is leaving.
// The session itself continues elsewhere; only its work on this lease ends.
func handleWorktreeExit(ctx context.Context, opts Options, p Payload, dir string) {
	exited := payloadPath(p, dir)
	root, ok := worktree.Root(exited)
	if !ok {
		return
	}
	taskID, ok := worktree.ParseDir(root)
	if !ok {
		return
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID, _ = markerSessionID(root)
	}
	if sessionID == "" {
		return
	}
	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	ectx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if err := c.EndAgentSession(ectx, taskID,
		cli.EndAgentSessionInput{Agent: agentName(), SessionID: sessionID}); err != nil {
		warn(opts, "end agent session on %s: %v", taskID, err)
	}
}
```

- [ ] **Step 5: Make `session-end` and `pre-commit` report**

`handleSessionEnd` currently takes `(opts, dir)` and makes no backbone call. Change its signature to `(ctx context.Context, opts Options, p Payload, dir string)` and close the session before removing the marker:

```go
func handleSessionEnd(ctx context.Context, opts Options, p Payload, dir string) {
	root, ok := worktree.Root(dir)
	if !ok {
		return
	}
	taskID, ok := worktree.ParseDir(root)
	if !ok {
		return
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID, _ = markerSessionID(root)
	}
	if sessionID != "" {
		if c, err := opts.client(); err != nil {
			warn(opts, "load config: %v", err)
		} else {
			ectx, cancel := context.WithTimeout(ctx, backboneTimeout)
			if err := c.EndAgentSession(ectx, taskID,
				cli.EndAgentSessionInput{Agent: agentName(), SessionID: sessionID}); err != nil {
				warn(opts, "end agent session on %s: %v", taskID, err)
			}
			cancel()
		}
	}
	if err := removeSessionMarker(root); err != nil {
		warn(opts, "remove session marker: %v", err)
	}
}
```

Note the guard change: the current `handleSessionEnd` discards the parsed task id (`if _, ok := worktree.ParseDir(root); !ok`); it now needs the id.

In `handlePreCommit`, after the existing `RenewLease` call, add an opportunistic heartbeat for agents that integrate only through git:

```go
	if heartbeatDue(root, opts.now()) {
		if sessionID, ok := markerSessionID(root); ok {
			reportSession(ctx, opts, c, taskID, root, sessionID)
		}
	}
```

- [ ] **Step 6: Wire the dispatch table**

In the guarded event set (`internal/hookrun/hookrun.go`, ~line 96), add:

```go
	"heartbeat":       true,
	"worktree-enter":  true,
	"worktree-exit":   true,
```

In `dispatch`, update the `session-end` call and add the three new cases:

```go
	case "session-end":
		handleSessionEnd(ctx, opts, p, dir)
	case "heartbeat":
		handleHeartbeat(ctx, opts, p, dir)
	case "worktree-enter":
		handleWorktreeEnter(ctx, opts, p, dir)
	case "worktree-exit":
		handleWorktreeExit(ctx, opts, p, dir)
```

In `internal/cmd/hook.go`, update the `Short` line to list the new events:

```go
		Short: "Run a Worklode lifecycle hook (session-start|heartbeat|session-end|pre-commit|worktree-create|worktree-remove|worktree-enter|worktree-exit)",
```

- [ ] **Step 7: Run the full test suite**

Run: `go test ./... `
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/hookrun/hookrun.go internal/hookrun/hookrun_test.go internal/cmd/hook.go
git commit -m "Report agent sessions from lifecycle hooks"
```

---

### Task 9: Document the hook bindings

The Claude Code plugin's `hooks.json` lives in the **claude-plugins** repo, not
here. This task documents the bindings worklode expects so that repo can be
updated; it changes no Go code.

**Files:**
- Modify: `README.md` (the hooks section around line 184)

- [ ] **Step 1: Document the events and their bindings**

In `README.md`, after the existing `install-git-hooks` paragraph (~line 186), add:

```markdown
### Agent session tracking

`lode hook` reports which coding-agent session is working a task, so the
backbone can show running sessions. Sessions are recorded against the task's
lease; a lease outlives many sessions.

The agent reporting itself is taken from `LODE_AGENT` (default `claude-code`).
Accepted values: `claude-code`, `codex`, `cursor`, `aider`, `opencode`, `pi`,
`amp`, `other`.

Claude Code bindings, for the plugin's `hooks.json`:

| `lode hook` event | Claude Code event |
|---|---|
| `session-start` | `SessionStart` |
| `heartbeat` | `Stop`, `StopFailure`, `SubagentStop`, `Notification` |
| `worktree-enter` | `PostToolUse` matcher `EnterWorktree` |
| `worktree-exit` | `PostToolUse` matcher `ExitWorktree` |
| `session-end` | `SessionEnd` |

Heartbeats are debounced to one per minute per worktree, so binding `Stop` is
cheap even in a fast conversation. Every hook stays bounded by the 2s backbone
timeout and never fails the event that triggered it.
```

- [ ] **Step 2: Verify the docs build/lint cleanly**

Run: `go build ./... && go vet ./...`
Expected: no output (README changes cannot break these, but this confirms the tree is still clean before the final commit).

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "Document agent session hook bindings"
```

---

## Notes for the implementer

**Helper names in test files.** Store and hookrun helpers were checked against
the tree and are named correctly in this plan (`createTask`,
`defaultTaskInput`, `openLeaseStore`, `newRealServer`, `initGitRepo`,
`setupLeasedWorktree`, `payloadJSON`, `newRecordingServer`). The two the plan
could not confirm are flagged at their use sites: `readyTask`/`decodeJSON` in
Task 5 and `newTestClient` in Task 6. Grep before writing; where one does not
exist, inline the two or three lines it stands for rather than inventing a
parallel helper.

**What is deliberately not built.** No token or cost computation (the columns
ship empty), no `lode sessions` command, no web UI. `Stop` and `SessionEnd`
carry `transcript_path`, which is where the cost work will start.
