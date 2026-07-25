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
// already-recorded path and emit no further events, and apply is not called —
// so the in-transaction lease re-check below only ever runs on the inserting
// call. Every later heartbeat is authorized solely by the non-transactional
// heldLease check above.
//
// A heartbeat on a closed row (ended_at set by EndAgentSession) reopens it:
// the caller working the lease again is still that same session. Since
// heldLease is read outside any transaction, it cannot rule out a release
// landing between that check and the UPDATE below; the UPDATE's own
// "lease still unreleased" EXISTS clause is what actually guards the reopen.
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

	_, inserted, err := s.RecordEvent(ctx, "cli", extID, "agent_session.started", nil,
		func(tx *sql.Tx, eventID int64) error {
			// Re-check inside the tx: the lease may have been released and
			// re-claimed since heldLease read it, which would make extID refer
			// to a different lease than the one being written. This guard only
			// runs here, on the inserting call — see the doc comment above.
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

	// The inserting call already wrote last_seen_at = now; only a heartbeat
	// on an existing row needs the UPDATE.
	if !inserted {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE agent_sessions SET last_seen_at = $1, ended_at = NULL
			  WHERE lease_id = $2 AND agent = $3 AND external_session_id = $4
			    AND EXISTS (SELECT 1 FROM leases WHERE id = $2 AND released_at IS NULL)`,
			now, lease.ID, agent, sessionID,
		); err != nil {
			return nil, fmt.Errorf("bump agent session last_seen_at: %w", err)
		}
	}

	return s.AgentSession(ctx, lease.ID, agent, sessionID)
}

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
// is stored as USD. A currency with no amount is accepted and stored, but
// leaves cost_amount untouched — cost_amount IS NULL is what means "no cost
// recorded yet", not the presence of a currency.
//
// Because a session can be closed, reopened by a later heartbeat, and closed
// again, the ended event's external id is minted fresh per call (like
// Claim/Renew/Release), not derived from the session's natural key.
// Idempotency instead comes from the UPDATE's own "AND ended_at IS NULL":
// a repeat close matches zero rows, apply returns ErrNotFound, and the
// whole transaction — including the event insert — rolls back, so a repeat
// close never leaves a duplicate agent_session.ended event.
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
