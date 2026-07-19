package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Lease grants one actor exclusive claim to a task until it expires or is
// released. At most one active (unreleased) lease exists per task — the
// leases_active partial unique index enforces this in the database.
type Lease struct {
	ID         int64
	TaskID     string
	ActorID    string
	SessionID  string
	AcquiredAt time.Time
	RenewedAt  time.Time
	ExpiresAt  time.Time
	ReleasedAt *time.Time
}

// DefaultLeaseTTL is used when Claim or Renew is called with ttl <= 0.
const DefaultLeaseTTL = 2 * time.Hour

// randomExternalID returns a random hex id used as the external id for
// server-generated CLI events (claim/renew/release have no upstream
// delivery id, so we mint one).
func randomExternalID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate event external id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// leaseColumns is the SELECT list scanLease expects, in order.
const leaseColumns = `id, task_id, actor_id, session_id, acquired_at, renewed_at, expires_at, released_at`

func scanLease(row rowScanner) (*Lease, error) {
	var l Lease
	var session, renewedAt, releasedAt sql.NullString
	var acquiredAt, expiresAt string
	if err := row.Scan(&l.ID, &l.TaskID, &l.ActorID, &session,
		&acquiredAt, &renewedAt, &expiresAt, &releasedAt); err != nil {
		return nil, err
	}
	l.SessionID = session.String
	var err error
	if l.AcquiredAt, err = time.Parse(time.RFC3339, acquiredAt); err != nil {
		return nil, fmt.Errorf("parse lease %d acquired_at: %w", l.ID, err)
	}
	if renewedAt.Valid {
		if l.RenewedAt, err = time.Parse(time.RFC3339, renewedAt.String); err != nil {
			return nil, fmt.Errorf("parse lease %d renewed_at: %w", l.ID, err)
		}
	}
	if l.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt); err != nil {
		return nil, fmt.Errorf("parse lease %d expires_at: %w", l.ID, err)
	}
	if releasedAt.Valid {
		t, err := time.Parse(time.RFC3339, releasedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse lease %d released_at: %w", l.ID, err)
		}
		l.ReleasedAt = &t
	}
	return &l, nil
}

// activeLeaseTx returns the active (unreleased) lease on taskID inside tx,
// or ErrNotFound if there is none.
func activeLeaseTx(tx *sql.Tx, taskID string) (*Lease, error) {
	row := tx.QueryRow(
		`SELECT `+leaseColumns+` FROM leases WHERE task_id = ? AND released_at IS NULL`, taskID)
	l, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no active lease on task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get active lease on %s: %w", taskID, err)
	}
	return l, nil
}

// Claim atomically leases taskID to actorID and moves the task from ready to
// in_progress, all inside one recorded "cli" event. Errors:
//
//   - ErrLeased: the task already has an active lease (the leases_active
//     unique index is the backstop for races).
//   - ErrBlocked: an open 'blocks' edge points at the task.
//   - ErrBadTransition: the task is not in state ready (draft, done, ...).
//   - ErrNotFound: the task or actor does not exist.
//
// ttl <= 0 means DefaultLeaseTTL.
func (s *Store) Claim(ctx context.Context, taskID, actorID, sessionID string, ttl time.Duration) (*Lease, error) {
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}

	var lease *Lease
	_, _, err = s.RecordEvent(ctx, "cli", extID, "lease.claimed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := s.nowFn().UTC().Truncate(time.Second)

			var one int
			if err := tx.QueryRow(`SELECT 1 FROM actors WHERE id = ?`, actorID).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("actor %s: %w", actorID, ErrNotFound)
				}
				return fmt.Errorf("check actor %s: %w", actorID, err)
			}

			var existing int64
			err := tx.QueryRow(
				`SELECT id FROM leases WHERE task_id = ? AND released_at IS NULL`, taskID,
			).Scan(&existing)
			if err == nil {
				return fmt.Errorf("task %s: %w", taskID, ErrLeased)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("check active lease on %s: %w", taskID, err)
			}

			blocked, err := IsBlocked(tx, taskID)
			if err != nil {
				return err
			}
			if blocked {
				return fmt.Errorf("task %s: %w", taskID, ErrBlocked)
			}

			// Unknown task → ErrNotFound; task not in ready → ErrBadTransition.
			if err := Transition(tx, now, taskID, "ready", "in_progress", eventID); err != nil {
				return err
			}

			var session sql.NullString
			if sessionID != "" {
				session = sql.NullString{String: sessionID, Valid: true}
			}
			expires := now.Add(ttl)
			nowStr := now.Format(time.RFC3339)
			res, err := tx.Exec(
				`INSERT INTO leases (task_id, actor_id, session_id, acquired_at, renewed_at, expires_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				taskID, actorID, session, nowStr, nowStr, expires.Format(time.RFC3339),
			)
			if err != nil {
				if strings.Contains(err.Error(), "UNIQUE constraint failed") {
					return fmt.Errorf("task %s: %w", taskID, ErrLeased)
				}
				return fmt.Errorf("insert lease on %s: %w", taskID, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("lease insert id: %w", err)
			}
			lease = &Lease{
				ID:         id,
				TaskID:     taskID,
				ActorID:    actorID,
				SessionID:  sessionID,
				AcquiredAt: now,
				RenewedAt:  now,
				ExpiresAt:  expires,
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return lease, nil
}

// Renew extends the active lease on taskID held by actorID: renewed_at is
// set to now and expires_at to now+ttl (ttl <= 0 means DefaultLeaseTTL).
//
// If there is no active lease held by actorID — either no lease at all, or
// the task is leased by a different actor — Renew returns ErrNotFound. The
// two cases are deliberately indistinguishable so a renewal attempt does not
// leak who holds the task; use ActiveLease to inspect the holder. An expired
// lease that the sweeper has not yet closed can still be renewed.
func (s *Store) Renew(ctx context.Context, taskID, actorID string, ttl time.Duration) (*Lease, error) {
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	extID, err := randomExternalID()
	if err != nil {
		return nil, err
	}

	var lease *Lease
	_, _, err = s.RecordEvent(ctx, "cli", extID, "lease.renewed", nil,
		func(tx *sql.Tx, eventID int64) error {
			l, err := activeLeaseTx(tx, taskID)
			if err != nil {
				return err
			}
			if l.ActorID != actorID {
				return fmt.Errorf("no active lease on task %s held by %s: %w", taskID, actorID, ErrNotFound)
			}
			now := s.nowFn().UTC().Truncate(time.Second)
			expires := now.Add(ttl)
			if _, err := tx.Exec(
				`UPDATE leases SET renewed_at = ?, expires_at = ? WHERE id = ?`,
				now.Format(time.RFC3339), expires.Format(time.RFC3339), l.ID,
			); err != nil {
				return fmt.Errorf("renew lease %d: %w", l.ID, err)
			}
			l.RenewedAt = now
			l.ExpiresAt = expires
			lease = l
			return nil
		})
	if err != nil {
		return nil, err
	}
	return lease, nil
}

// Release closes the active lease on taskID held by actorID (released_at =
// now) and, if the task is still in_progress, moves it back to ready. If the
// task has already moved on (e.g. to in_review), only the lease is closed —
// task state is left alone. A non-holder gets ErrNotFound, same policy as
// Renew.
func (s *Store) Release(ctx context.Context, taskID, actorID string) error {
	extID, err := randomExternalID()
	if err != nil {
		return err
	}

	_, _, err = s.RecordEvent(ctx, "cli", extID, "lease.released", nil,
		func(tx *sql.Tx, eventID int64) error {
			l, err := activeLeaseTx(tx, taskID)
			if err != nil {
				return err
			}
			if l.ActorID != actorID {
				return fmt.Errorf("no active lease on task %s held by %s: %w", taskID, actorID, ErrNotFound)
			}
			now := s.nowFn().UTC().Truncate(time.Second)
			return closeLease(tx, now, l.ID, taskID, eventID)
		})
	return err
}

// closeLease sets released_at on the lease and, when the task is still
// in_progress, transitions it back to ready. Shared by Release and
// ExpireLeases.
func closeLease(tx *sql.Tx, now time.Time, leaseID int64, taskID string, eventID int64) error {
	if _, err := tx.Exec(
		`UPDATE leases SET released_at = ? WHERE id = ?`,
		now.UTC().Format(time.RFC3339), leaseID,
	); err != nil {
		return fmt.Errorf("close lease %d: %w", leaseID, err)
	}
	var state string
	if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		return fmt.Errorf("get task %s state: %w", taskID, err)
	}
	if state == "in_progress" {
		return Transition(tx, now, taskID, "in_progress", "ready", eventID)
	}
	return nil
}

// CloseActiveLease sets released_at on the active lease on taskID, no matter
// who holds it, inside the given transaction. A task with no active lease is
// a no-op. Unlike closeLease it never touches task state — callers (done,
// abandon, merge) set the task's state themselves in the same transaction.
func CloseActiveLease(tx *sql.Tx, now time.Time, taskID string) error {
	if _, err := tx.Exec(
		`UPDATE leases SET released_at = ? WHERE task_id = ? AND released_at IS NULL`,
		now.UTC().Format(time.RFC3339), taskID,
	); err != nil {
		return fmt.Errorf("close active lease on %s: %w", taskID, err)
	}
	return nil
}

// ActiveLease returns the active (unreleased) lease on taskID, or
// ErrNotFound if there is none.
func (s *Store) ActiveLease(ctx context.Context, taskID string) (*Lease, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+leaseColumns+` FROM leases WHERE task_id = ? AND released_at IS NULL`, taskID)
	l, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no active lease on task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get active lease on %s: %w", taskID, err)
	}
	return l, nil
}

// ExpireLeases closes every active lease whose expires_at is before now and
// moves each affected task back from in_progress to ready (a task that has
// already left in_progress keeps its state; the lease is still closed).
// Every expiry is its own "system" event with external id
// "lease-expired-<leaseID>", so re-running the sweep never double-applies.
// Returns the number of leases newly expired. Callers pass now explicitly
// (the serve loop passes s.nowFn()) so sweeps are testable and comparisons
// are consistent within one run.
func (s *Store) ExpireLeases(ctx context.Context, now time.Time) (int, error) {
	nowStr := now.UTC().Format(time.RFC3339)

	// RFC3339 UTC strings compare lexicographically in time order.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id FROM leases WHERE released_at IS NULL AND expires_at < ? ORDER BY id`,
		nowStr)
	if err != nil {
		return 0, fmt.Errorf("find expired leases: %w", err)
	}
	type expired struct {
		leaseID int64
		taskID  string
	}
	var candidates []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.leaseID, &e.taskID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired lease: %w", err)
		}
		candidates = append(candidates, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("find expired leases: %w", err)
	}
	rows.Close()

	count := 0
	for _, e := range candidates {
		extID := fmt.Sprintf("lease-expired-%d", e.leaseID)
		_, inserted, err := s.RecordEvent(ctx, "system", extID, "lease.expired", nil,
			func(tx *sql.Tx, eventID int64) error {
				// Re-check inside the tx: a release may have won the race.
				var stillActive int
				err := tx.QueryRow(
					`SELECT 1 FROM leases WHERE id = ? AND released_at IS NULL`, e.leaseID,
				).Scan(&stillActive)
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				if err != nil {
					return fmt.Errorf("recheck lease %d: %w", e.leaseID, err)
				}
				return closeLease(tx, now, e.leaseID, e.taskID, eventID)
			})
		if err != nil {
			return count, err
		}
		if inserted {
			count++
		}
	}
	return count, nil
}
