# Agent Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record which coding-agent session is working each leased task, so the backbone can report running sessions and later attribute token cost.

**Architecture:** A new `agent_sessions` child table hangs off `leases` (a lease outlives many sessions). Two store functions — `TouchAgentSession` (start-or-heartbeat, idempotent through a deterministic event external id) and `EndAgentSession` — are exposed as two POST endpoints and driven by `lode hook` events bound to Claude Code's `SessionStart`, `Stop`/`StopFailure`/`SubagentStop`/`Notification`, `PostToolUse(EnterWorktree)` and `SessionEnd`.

**Tech Stack:** Go 1.x, Postgres (database/sql + pgx stdlib), golang-migrate, cobra, net/http `ServeMux`.

**Spec:** `docs/specs/2026-07-25-agent-sessions-design.md`

**Prerequisite:** store tests need Postgres. Run `docker compose up -d postgres` once before starting; tests skip (not fail) if it is unreachable and `CI` is unset.

---

### Task 1: Migration 0004 — the `agent_sessions` table

**Files:**
- Create: `deploy/base/migrations/0004_agent_sessions.up.sql`
- Create: `deploy/base/migrations/0004_agent_sessions.down.sql`
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

Create `deploy/base/migrations/0004_agent_sessions.up.sql`:

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
                          CONSTRAINT agent_sessions_cost_currency_format
                          CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT agent_sessions_lease_session_unique
      UNIQUE (lease_id, agent, external_session_id)
);
```

The `agent` CHECK is likewise named `agent_sessions_agent_known`. No separate
index on `lease_id`: the unique constraint's index already leads with that
column.

Create `deploy/base/migrations/0004_agent_sessions.down.sql`:

```sql
DROP TABLE agent_sessions;
```

- [ ] **Step 4: Register the migration with kustomize**

In `deploy/base/kustomization.yaml`, add two lines to the `worklode-migrations` file list, after `migrations/0003_project_keys.down.sql`:

```yaml
      - migrations/0004_agent_sessions.up.sql
      - migrations/0004_agent_sessions.down.sql
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestAgentSessionsSchema -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0004_agent_sessions.up.sql \
        deploy/base/migrations/0004_agent_sessions.down.sql \
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

Also makes a heartbeat re-open a closed session, which only becomes reachable
once `EndAgentSession` exists: a session that exits a worktree and later
re-enters it is working that lease again and must not stay closed.

**Files:**
- Modify: `internal/store/agent_sessions.go` (append, plus the heartbeat UPDATE)
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
	// Random, not derived from the session: a session can legitimately be
	// closed more than once on one lease (exit, re-enter, exit). Idempotency
	// comes from the ended_at IS NULL predicate below, which fails apply and
	// rolls the event back on a repeat close.
	extID, err := randomExternalID()
	if err != nil {
		return err
	}

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

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// claimedTask creates a project and a task and claims it as alice, returning
// the task id. Built from the same helpers TestClaim uses in lifecycle_test.go.
func claimedTask(t *testing.T, st *store.Store, h http.Handler, token string) string {
	t.Helper()
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Agent session task", "priority": "high", "kind": "bug",
	})
	id, _ := task["id"].(string)
	if id == "" {
		t.Fatalf("created task has no id: %v", task)
	}
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token,
		map[string]any{"worktree": "host:/wt/one"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim: got %d, body %s", rr.Code, rr.Body.String())
	}
	return id
}

func TestAgentSessionEndpoints(t *testing.T) {
	st, h, token := newTestServer(t)
	id := claimedTask(t, st, h, token)

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
	decodeInto(t, rr, &got)
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
	id := claimedTask(t, st, h, token)

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

Helper names above were verified against the tree: `newTestServer`
(`internal/api/server_test.go:26`), `doReq` (`:58`), `decodeMap` (`:77`),
`decodeInto` (`:88`), `createProject` (`internal/api/tasks_test.go:18`),
`createTaskViaAPI` (`:29`), `secondActor` (`internal/api/lifecycle_test.go:16`).
A task created through the API is immediately claimable, as `TestClaim` shows.

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

Append to `internal/cli/client_test.go`. That file drives a **real** server, not a
stub: `newTestServer(t)` (line 28) returns `(*store.Store, *cli.Client, string)`
with a live `httptest` listener, and tests exercise the client end to end — see
`TestClientBriefAndRebindWorktree` (line 369). Follow that pattern; the package
is `cli_test`, so exported types are qualified `cli.…`.

```go
func TestClientAgentSession(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()

	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Agent session task", Priority: "high", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := c.ClaimTask(ctx, task.ID, "host:/wt-1", 0); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	sess, _, err := c.TouchAgentSession(ctx, task.ID, "claude-code", "2.0.1", "sess-1")
	if err != nil {
		t.Fatalf("TouchAgentSession: %v", err)
	}
	if sess.Agent != "claude-code" || sess.SessionID != "sess-1" || sess.AgentVersion != "2.0.1" {
		t.Fatalf("TouchAgentSession = %+v", sess)
	}
	if sess.LeaseID == 0 {
		t.Fatalf("TouchAgentSession lease id = 0: %+v", sess)
	}
	if sess.EndedAt != nil {
		t.Fatalf("a new session is already ended: %+v", sess)
	}
	if sess.LastSeenAt.Before(sess.StartedAt) {
		t.Fatalf("last_seen_at before started_at: %+v", sess)
	}

	if err := c.EndAgentSession(ctx, task.ID,
		cli.EndAgentSessionInput{Agent: "claude-code", SessionID: "sess-1"}); err != nil {
		t.Fatalf("EndAgentSession: %v", err)
	}

	// A session that was never reported cannot be ended.
	err = c.EndAgentSession(ctx, task.ID,
		cli.EndAgentSessionInput{Agent: "claude-code", SessionID: "never-seen"})
	if err == nil {
		t.Fatal("EndAgentSession on an unknown session id succeeded")
	}
}
```

Note that ending an *already-ended* session is deliberately not asserted: the
end event's external id is deterministic, so a repeat call takes `RecordEvent`'s
already-recorded path and returns nil. That is the intended idempotency, not a
bug.

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

### Task 10: `lode claude install` / `lode claude uninstall`

Task 9 documents the bindings; this task installs them. `lode claude install`
writes Worklode's hook bindings into the repo's Claude Code settings file so a
developer does not hand-edit JSON, and `lode claude uninstall` takes them out
again. Both converge — safe to re-run, and neither disturbs settings Worklode
did not write.

**Files:**
- Create: `internal/cmd/claude.go`
- Create: `internal/cmd/claude_test.go`
- Modify: `README.md` (extend the "Agent session tracking" section from Task 9)

**Scope → settings file** (both under the git worktree root, resolved with
`worktree.Root`, the same way `installhooks.go` does it):

| `--scope` | File |
|---|---|
| `local` (default) | `<root>/.claude/settings.local.json` |
| `project` | `<root>/.claude/settings.json` |

**Bindings installed** — every `lode hook` event that has a Claude Code
counterpart:

| Claude Code event | Matcher | Command |
|---|---|---|
| `SessionStart` | — | `lode hook session-start` |
| `SessionEnd` | — | `lode hook session-end` |
| `Stop` | — | `lode hook heartbeat` |
| `StopFailure` | — | `lode hook heartbeat` |
| `SubagentStop` | — | `lode hook heartbeat` |
| `Notification` | — | `lode hook heartbeat` |
| `WorktreeCreate` | — | `lode hook worktree-create` |
| `WorktreeRemove` | — | `lode hook worktree-remove` |
| `PostToolUse` | `EnterWorktree` | `lode hook worktree-enter` |

**Ownership marker.** JSON has no comments, so a Worklode-installed hook is
identified by its command: any entry whose `command` begins with `lode hook `.
Install strips every such entry before writing the current set (so a re-run
converges instead of duplicating, and a removed binding disappears); uninstall
strips them and nothing else.

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/claude_test.go`:

```go
package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readSettings reads a settings file as generic JSON, failing the test if it
// is missing or malformed.
func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// commandsFor returns every hook command registered for a Claude Code event.
func commandsFor(t *testing.T, settings map[string]any, event string) []string {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	groups, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		entries, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := entry["command"].(string); ok {
				out = append(out, cmd)
			}
		}
	}
	return out
}

func TestClaudeInstallScopes(t *testing.T) {
	root := t.TempDir()

	local, err := claudeSettingsPath(root, scopeLocal)
	if err != nil {
		t.Fatalf("local path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); local != want {
		t.Fatalf("local scope path: got %s, want %s", local, want)
	}
	project, err := claudeSettingsPath(root, scopeProject)
	if err != nil {
		t.Fatalf("project path: %v", err)
	}
	if want := filepath.Join(root, ".claude", "settings.json"); project != want {
		t.Fatalf("project scope path: got %s, want %s", project, want)
	}
	if _, err := claudeSettingsPath(root, "global"); err == nil {
		t.Fatal("unknown scope was accepted")
	}
}

func TestClaudeInstallWritesBindings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.local.json")

	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("install: %v", err)
	}

	settings := readSettings(t, path)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 1 || got[0] != "lode hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
	if got := commandsFor(t, settings, "Stop"); len(got) != 1 || got[0] != "lode hook heartbeat" {
		t.Fatalf("Stop commands: %v", got)
	}
	// PostToolUse is matched on a tool name, so it costs nothing per
	// ordinary tool call.
	got := commandsFor(t, settings, "PostToolUse")
	if len(got) != 1 || got[0] != "lode hook worktree-enter" {
		t.Fatalf("PostToolUse commands: %v", got)
	}
	hooks := settings["hooks"].(map[string]any)
	groups := hooks["PostToolUse"].([]any)
	if len(groups) != 1 {
		t.Fatalf("PostToolUse groups: %v, want 1", groups)
	}
	if m := groups[0].(map[string]any)["matcher"]; m != "EnterWorktree" {
		t.Fatalf("PostToolUse matcher: %v, want EnterWorktree", m)
	}
}

func TestClaudeInstallIsIdempotentAndPreservesForeignSettings(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "settings.local.json")
	existing := `{
	  "permissions": {"allow": ["Bash(go test:*)"]},
	  "hooks": {
	    "Stop": [{"hooks": [{"type": "command", "command": "my-own-tool --report"}]}]
	  }
	}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first install: %v", err)
	}
	if err := installClaudeHooks(path); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second install: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("install is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	settings := readSettings(t, path)
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("install dropped the unrelated permissions block")
	}
	stop := commandsFor(t, settings, "Stop")
	if len(stop) != 2 {
		t.Fatalf("Stop commands: %v, want the foreign hook plus ours", stop)
	}

	if err := uninstallClaudeHooks(path); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	settings = readSettings(t, path)
	if _, ok := settings["permissions"]; !ok {
		t.Fatal("uninstall dropped the unrelated permissions block")
	}
	if got := commandsFor(t, settings, "Stop"); len(got) != 1 || got[0] != "my-own-tool --report" {
		t.Fatalf("Stop after uninstall: %v, want only the foreign hook", got)
	}
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 0 {
		t.Fatalf("SessionStart after uninstall: %v, want none", got)
	}
}

func TestClaudeUninstallWithNoSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if err := uninstallClaudeHooks(path); err != nil {
		t.Fatalf("uninstall with no settings file: %v, want nil", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("uninstall created a settings file")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cmd/ -run TestClaude -v`
Expected: FAIL — `undefined: claudeSettingsPath`, `undefined: installClaudeHooks`.

- [ ] **Step 3: Write the implementation**

Create `internal/cmd/claude.go`:

```go
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// Settings scopes. Local settings are the developer's own and are normally
// git-ignored; project settings are committed and shared by the whole repo.
const (
	scopeLocal   = "local"
	scopeProject = "project"
)

// lodeHookPrefix marks a settings entry as Worklode's. JSON has no comments,
// so the command itself is the marker: install strips every entry with this
// prefix before writing the current set, which makes a re-run converge rather
// than duplicate.
const lodeHookPrefix = "lode hook "

// claudeBinding is one Claude Code hook binding. An empty Matcher means the
// binding applies to every occurrence of the event.
type claudeBinding struct {
	Event   string
	Matcher string
	Command string
}

// claudeBindings is every Claude Code event Worklode listens to. Heartbeat is
// bound to four events because Stop alone leaves a live session looking dead:
// StopFailure replaces Stop when a turn dies on an API error, SubagentStop
// covers a long subagent fan-out, and Notification covers a session blocked on
// a human.
var claudeBindings = []claudeBinding{
	{Event: "SessionStart", Command: "lode hook session-start"},
	{Event: "SessionEnd", Command: "lode hook session-end"},
	{Event: "Stop", Command: "lode hook heartbeat"},
	{Event: "StopFailure", Command: "lode hook heartbeat"},
	{Event: "SubagentStop", Command: "lode hook heartbeat"},
	{Event: "Notification", Command: "lode hook heartbeat"},
	{Event: "WorktreeCreate", Command: "lode hook worktree-create"},
	{Event: "WorktreeRemove", Command: "lode hook worktree-remove"},
	{Event: "PostToolUse", Matcher: "EnterWorktree", Command: "lode hook worktree-enter"},
}

func init() {
	rootCmd.AddCommand(newClaudeCmd())
}

func newClaudeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Manage Worklode's Claude Code integration",
	}
	cmd.AddCommand(newClaudeInstallCmd(), newClaudeUninstallCmd())
	return cmd
}

func newClaudeInstallCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Worklode's hooks into this repo's Claude Code settings",
		Long: "Writes Worklode's lifecycle hook bindings (session start/end, heartbeat, " +
			"worktree enter/exit/create/remove) into the repo's Claude Code settings file. " +
			"Safe to re-run: it replaces Worklode's own entries and leaves every other " +
			"setting untouched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPathForScope(scope)
			if err != nil {
				return err
			}
			if err := installClaudeHooks(path); err != nil {
				return err
			}
			return reportClaudeCmd(cmd, "installed", path)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", scopeLocal,
		"which settings file to write: local (settings.local.json) or project (settings.json)")
	return cmd
}

func newClaudeUninstallCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Worklode's hooks from this repo's Claude Code settings",
		Long: "Removes every `lode hook` binding from the repo's Claude Code settings file, " +
			"leaving all other settings — including third-party hooks on the same events — " +
			"in place. A missing settings file is not an error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := settingsPathForScope(scope)
			if err != nil {
				return err
			}
			if err := uninstallClaudeHooks(path); err != nil {
				return err
			}
			return reportClaudeCmd(cmd, "uninstalled", path)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", scopeLocal,
		"which settings file to write: local (settings.local.json) or project (settings.json)")
	return cmd
}

// reportClaudeCmd prints the outcome in whichever form the caller asked for.
func reportClaudeCmd(cmd *cobra.Command, action, path string) error {
	if jsonOut(cmd) {
		b, err := json.Marshal(struct {
			Action string `json:"action"`
			Path   string `json:"path"`
		}{Action: action, Path: path})
		if err != nil {
			return err
		}
		printRaw(cmd, b)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s Worklode hooks in %s\n", action, path)
	return nil
}

// settingsPathForScope resolves the settings file for scope, relative to the
// git worktree root of the current directory.
func settingsPathForScope(scope string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	root, ok := worktree.Root(cwd)
	if !ok {
		return "", fmt.Errorf("not inside a git repository: %s", cwd)
	}
	return claudeSettingsPath(root, scope)
}

// claudeSettingsPath maps a scope to its settings file under root.
func claudeSettingsPath(root, scope string) (string, error) {
	switch scope {
	case scopeLocal:
		return filepath.Join(root, ".claude", "settings.local.json"), nil
	case scopeProject:
		return filepath.Join(root, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("unknown scope %q: want %q or %q", scope, scopeLocal, scopeProject)
	}
}

// readSettingsFile reads path as generic JSON. A missing file is an empty
// settings object, not an error — installing into a repo that has never had
// Claude Code settings is the common case.
func readSettingsFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return settings, nil
}

// writeSettingsFile writes settings back to path, creating the .claude
// directory if needed. Output is indented and newline-terminated so a
// committed settings file stays readable and diffs cleanly.
func writeSettingsFile(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// installClaudeHooks writes Worklode's bindings into the settings file at
// path, replacing any bindings a previous install left behind and preserving
// every other setting.
func installClaudeHooks(path string) error {
	settings, err := readSettingsFile(path)
	if err != nil {
		return err
	}
	hooks := stripLodeHooks(settingsHooks(settings))
	for _, b := range claudeBindings {
		hooks[b.Event] = appendBinding(hooks[b.Event], b)
	}
	settings["hooks"] = hooks
	return writeSettingsFile(path, settings)
}

// uninstallClaudeHooks removes Worklode's bindings from the settings file at
// path. A missing file is a no-op: there is nothing to uninstall.
func uninstallClaudeHooks(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	settings, err := readSettingsFile(path)
	if err != nil {
		return err
	}
	hooks := stripLodeHooks(settingsHooks(settings))
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return writeSettingsFile(path, settings)
}

// settingsHooks returns the settings' "hooks" object, or an empty one when it
// is absent or not an object.
func settingsHooks(settings map[string]any) map[string]any {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return hooks
}

// appendBinding adds b to an event's existing group list, which may be nil or
// a non-list left by hand-editing (in which case it is replaced).
func appendBinding(existing any, b claudeBinding) []any {
	groups, _ := existing.([]any)
	group := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": b.Command}},
	}
	if b.Matcher != "" {
		group["matcher"] = b.Matcher
	}
	return append(groups, group)
}

// stripLodeHooks removes every `lode hook` entry from a hooks object, dropping
// groups and events that end up empty so an uninstall leaves no residue. Any
// third-party hook sharing an event is preserved.
func stripLodeHooks(hooks map[string]any) map[string]any {
	out := map[string]any{}
	for event, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			// Not a shape we wrote; leave it exactly as found.
			out[event] = raw
			continue
		}
		var kept []any
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			entries, ok := group["hooks"].([]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			var keptEntries []any
			for _, e := range entries {
				if isLodeHookEntry(e) {
					continue
				}
				keptEntries = append(keptEntries, e)
			}
			if len(keptEntries) == 0 {
				continue
			}
			group["hooks"] = keptEntries
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			continue
		}
		out[event] = kept
	}
	return out
}

// isLodeHookEntry reports whether one hook entry runs a `lode hook` command.
func isLodeHookEntry(e any) bool {
	entry, ok := e.(map[string]any)
	if !ok {
		return false
	}
	command, ok := entry["command"].(string)
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(command), lodeHookPrefix)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/ -run TestClaude -v`
Expected: PASS (all four tests)

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Document the commands**

In `README.md`, at the end of the "Agent session tracking" section added in
Task 9, replace the sentence "Claude Code bindings, for the plugin's
`hooks.json`:" heading paragraph with a pointer to the command, and append:

```markdown
Install the bindings into a repo with:

```
lode claude install                    # ~/…/<repo>/.claude/settings.local.json
lode claude install --scope project    # ~/…/<repo>/.claude/settings.json
```

`lode claude uninstall` (same `--scope` flag) removes them again. Both are
idempotent and only touch entries whose command starts with `lode hook`, so
third-party hooks on the same events are left alone.
```

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/claude.go internal/cmd/claude_test.go README.md
git commit -m "Add lode claude install/uninstall"
```

---

## Notes for the implementer

**Helper names in test files.** Every helper this plan calls has been checked
against the tree: store (`createTask`, `defaultTaskInput`, `openLeaseStore`,
`leaseTestNow`), hookrun (`newRealServer`, `newRecordingServer`, `initGitRepo`,
`setupLeasedWorktree`, `payloadJSON`), api (`newTestServer`, `doReq`,
`decodeMap`, `decodeInto`, `createProject`, `createTaskViaAPI`, `secondActor`)
and cli (`newTestServer`). Still grep before writing — if a signature has drifted,
adapt the call site rather than adding a parallel helper.

**Migration numbering.** The plan originally said 0003; `0003_project_keys`
landed on main first, so the agent-sessions migration is **0004**.

**What is deliberately not built.** No token or cost computation (the columns
ship empty), no `lode sessions` command, no web UI. `Stop` and `SessionEnd`
carry `transcript_path`, which is where the cost work will start.
