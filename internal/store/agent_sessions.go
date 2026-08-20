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
//
// AgentSession is deliberately not model.AgentSession: ID is the database
// primary key this package needs internally (row updates) that never crosses
// the wire, so it stays outside the eleven fields model.AgentSession declares
// (ADR 036 §3, "store scan plumbing"). api.toAgentSessionJSON is the one
// conversion point from this type to model.AgentSession.
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

// costAmountRE mirrors the cost_amount column type, numeric(12,6): at most 6
// integer digits and at most 6 fractional digits, no sign — a cost is never
// negative.
var costAmountRE = regexp.MustCompile(`^\d{1,6}(\.\d{1,6})?$`)

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
// heldLease is read outside any transaction, so it cannot rule out a lease
// close landing between that read and the write that follows — on the
// inserting call, or on a heartbeat that reopens a closed row (ended_at set
// by EndAgentSession, cleared here because the caller working the lease
// again is still that same session). Both writes guard against this the same
// way: activeLeaseTxForShare and bumpAgentSessionLastSeen each take a
// FOR SHARE lock on the lease before writing, so a concurrent close either
// blocks until this call commits, or wins the race and makes the re-check
// fail.
//
// Buckets, when non-nil, replaces the session's recorded usage the same way
// EndAgentSession's does. A session that never ends cleanly — a crashed agent,
// or a lease the sweeper expires — is closed with no usage at all, so the
// heartbeat is the only place its spend can land. The write is
// replace-not-accumulate, so reporting the same running total every minute is
// safe to repeat and the last report before the crash is what survives.
//
// Errors: ErrInvalidInput for an unknown agent or empty session id,
// ErrNotFound when actorID does not hold an active lease on taskID.
func (s *Store) TouchAgentSession(ctx context.Context, taskID, actorID, agent, agentVersion, sessionID string, buckets []SessionUsageBucket) (*AgentSession, error) {
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
			// Re-check inside the tx, with a FOR SHARE lock: the lease may have
			// been released (or released and re-claimed) since heldLease read
			// it, which would either orphan this insert against a closed lease
			// or make extID refer to a different lease than the one being
			// written. This guard only runs here, on the inserting call — see
			// the doc comment above.
			cur, err := activeLeaseTxForShare(tx, taskID)
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
				lease.ID, agent, nullText(agentVersion), sessionID, now,
			); err != nil {
				return fmt.Errorf("insert agent session on lease %d: %w", lease.ID, err)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}

	// The inserting call already wrote last_seen_at = now; only a heartbeat
	// on an existing row needs the bump.
	if !inserted {
		if err := s.bumpAgentSessionLastSeen(ctx, lease.ID, agent, sessionID, now); err != nil {
			return nil, err
		}
	}

	if buckets != nil {
		if err := s.replaceAgentSessionUsage(ctx, lease.ID, agent, sessionID, buckets); err != nil {
			return nil, err
		}
	}

	return s.AgentSession(ctx, lease.ID, agent, sessionID)
}

// replaceAgentSessionUsage records buckets as the complete usage of the
// session identified by (leaseID, agent, sessionID), in its own transaction.
//
// It runs after the touch above rather than inside it because the touch's
// event apply only fires on the inserting call: a heartbeat takes RecordEvent's
// already-recorded path, so usage riding that apply would be written once per
// session and never again — the opposite of what the heartbeat is for.
//
// A row that is not there is not an error. The only way to reach that is a
// lease close landing between the touch and here, which already ended the
// session; there is nothing left to attribute the tokens to.
func (s *Store) replaceAgentSessionUsage(ctx context.Context, leaseID int64, agent, sessionID string, buckets []SessionUsageBucket) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var rowID int64
		err := tx.QueryRow(
			`SELECT id FROM agent_sessions
			  WHERE lease_id = $1 AND agent = $2 AND external_session_id = $3`,
			leaseID, agent, sessionID).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find agent session %s/%s on lease %d: %w", agent, sessionID, leaseID, err)
		}
		return applySessionUsageTx(ctx, tx, rowID, buckets)
	})
}

// bumpAgentSessionLastSeen advances last_seen_at (and clears ended_at, so a
// heartbeat reopens a closed session). The guard against racing a lease close
// is the FOR SHARE read: it blocks until any concurrent closeLease or
// CloseActiveLease commits, then sees released_at set and skips the UPDATE,
// so a session that close just ended stays ended.
func (s *Store) bumpAgentSessionLastSeen(ctx context.Context, leaseID int64, agent, sessionID string, now time.Time) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRow(
			`SELECT 1 FROM leases WHERE id = $1 AND released_at IS NULL FOR SHARE`, leaseID,
		).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			// Lease closed (or gone) between heldLease and here: the close
			// already ended this session. Nothing to bump.
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock lease %d for heartbeat: %w", leaseID, err)
		}
		if _, err := tx.Exec(
			`UPDATE agent_sessions SET last_seen_at = $1, ended_at = NULL
			  WHERE lease_id = $2 AND agent = $3 AND external_session_id = $4`,
			now, leaseID, agent, sessionID,
		); err != nil {
			return fmt.Errorf("bump agent session last_seen_at: %w", err)
		}
		return nil
	})
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

	// Buckets is the per-day, per-model, per-speed breakdown the session
	// actually billed. When it is non-nil it supersedes the four scalars
	// above: those are derived from it, and the server prices it rather than
	// trusting a client-computed amount. A non-nil empty slice is meaningful —
	// it clears the session's recorded usage.
	//
	// The breakdown is what makes cost correct rather than approximate: a
	// session mixes models at several-fold different rates, and each model's
	// prompt splits into cache classes priced from 0.1x to 2x base input.
	Buckets []SessionUsageBucket
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
	if usage.CostAmount != nil && !costAmountRE.MatchString(*usage.CostAmount) {
		return fmt.Errorf("cost amount %q is not a valid numeric(12,6) value: %w", *usage.CostAmount, ErrInvalidInput)
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
			var rowID int64
			err := tx.QueryRow(
				`UPDATE agent_sessions
				    SET ended_at      = $1,
				        input_tokens  = COALESCE($2, input_tokens),
				        output_tokens = COALESCE($3, output_tokens),
				        cost_amount   = COALESCE($4, cost_amount),
				        cost_currency = CASE WHEN $4 IS NULL THEN cost_currency ELSE $5 END
				  WHERE lease_id = $6 AND agent = $7 AND external_session_id = $8
				    AND ended_at IS NULL
				RETURNING id`,
				now, usage.InputTokens, usage.OutputTokens, usage.CostAmount, currency,
				lease.ID, agent, sessionID).Scan(&rowID)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("no open agent session %s/%s on task %s: %w",
					agent, sessionID, taskID, ErrNotFound)
			}
			if err != nil {
				return fmt.Errorf("end agent session %s/%s: %w", agent, sessionID, err)
			}
			if usage.Buckets == nil {
				return nil
			}
			return applySessionUsageTx(ctx, tx, rowID, usage.Buckets)
		})
	return err
}

// applySessionUsageTx records a session's per-model breakdown, rolls it up
// onto the session row, and rebuilds the affected days of the owning
// project's daily cost — all inside the caller's transaction, so the detail,
// the session summary, and the rollup can never disagree.
func applySessionUsageTx(ctx context.Context, tx *sql.Tx, rowID int64, buckets []SessionUsageBucket) error {
	totals, cost, currency, days, err := replaceSessionUsageTx(ctx, tx, rowID, buckets)
	if err != nil {
		return err
	}

	// input_tokens is every token the session put into a prompt — uncached
	// input, cache writes, and cache reads together. The class split that
	// determines what those tokens cost lives in agent_session_usage; this is
	// the headline volume, not a billing quantity.
	//
	// cost_amount is left NULL when priced buckets disagreed on a currency
	// (replaceSessionUsageTx returns an empty currency): one scalar cannot
	// honestly carry two.
	var amount, amountCurrency any
	if currency != "" {
		amount, amountCurrency = cost, currency
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE agent_sessions
		    SET input_tokens  = $1,
		        output_tokens = $2,
		        cost_amount   = $3,
		        cost_currency = COALESCE($4, cost_currency)
		  WHERE id = $5`,
		totals.Total()-totals.Output, totals.Output, amount, amountCurrency, rowID,
	); err != nil {
		return fmt.Errorf("roll up usage onto agent session %d: %w", rowID, err)
	}

	if len(days) == 0 {
		return nil
	}
	projectID, err := projectForSessionTx(ctx, tx, rowID)
	if err != nil {
		return err
	}
	return recomputeProjectDailyCostTx(ctx, tx, projectID, days)
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

// AgentSessionsForLease returns every session recorded against leaseID,
// oldest-started-first, or an empty slice for a lease with none.
func (s *Store) AgentSessionsForLease(ctx context.Context, leaseID int64) ([]AgentSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+agentSessionColumns+` FROM agent_sessions
		  WHERE lease_id = $1 ORDER BY started_at`,
		leaseID)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions for lease %d: %w", leaseID, err)
	}
	sessions, err := collectRows(rows, fmt.Sprintf("list agent sessions for lease %d", leaseID), byValue(scanAgentSession))
	if err != nil {
		return nil, err
	}
	return nonNil(sessions), nil
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
