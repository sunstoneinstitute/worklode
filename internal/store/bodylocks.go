package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Lock sweep thresholds (WL-REQ-1868).
const (
	lockIdleAfter   = time.Hour
	lockExpireAfter = 24 * time.Hour
)

// ErrLockHeld means another holder has the lock; Lock names them.
type ErrLockHeld struct{ Lock model.BodyLock }

func (e *ErrLockHeld) Error() string {
	return fmt.Sprintf("%s %s is locked by %s", e.Lock.Kind, e.Lock.SubjectID, e.Lock.Holder)
}

// ErrLockLost means the session no longer holds the lock.
type ErrLockLost struct{ Lost model.LockLost }

func (e *ErrLockLost) Error() string {
	return fmt.Sprintf("lock session ended: %s", e.Lost.Reason)
}

func checkLockKind(kind string) error {
	switch kind {
	case "doc", "rule", "task":
		return nil
	}
	return &FieldError{Field: "kind", Msg: "must be doc, rule or task"}
}

// TakeLock gives holder a new session on the subject's lock, creating the
// lock when it is free. The same holder may take it again (a second tab). It
// returns *ErrLockHeld when someone else holds it. CanUnlock is the caller's
// to fill.
func (s *Store) TakeLock(ctx context.Context, kind, subjectID, holder string) (model.BodyLock, error) {
	if err := checkLockKind(kind); err != nil {
		return model.BodyLock{}, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return model.BodyLock{}, fmt.Errorf("generate lock session id: %w", err)
	}
	session := hex.EncodeToString(b)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.BodyLock{}, fmt.Errorf("take lock: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// One statement decides the race: the conflict arm only matches the
	// current holder, so a third party gets no row back.
	var got string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO body_locks (kind, subject_id, holder) VALUES ($1, $2, $3)
		 ON CONFLICT (kind, subject_id) DO UPDATE SET holder = body_locks.holder
		   WHERE body_locks.holder = EXCLUDED.holder
		 RETURNING holder`, kind, subjectID, holder).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		l, gerr := s.GetLock(ctx, kind, subjectID)
		if gerr != nil {
			return model.BodyLock{}, gerr
		}
		if l == nil { // released between the two statements
			return s.TakeLock(ctx, kind, subjectID, holder)
		}
		return model.BodyLock{}, &ErrLockHeld{Lock: *l}
	}
	if err != nil {
		return model.BodyLock{}, fmt.Errorf("take lock: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO body_lock_sessions (id, kind, subject_id, holder) VALUES ($1, $2, $3, $4)`,
		session, kind, subjectID, holder); err != nil {
		return model.BodyLock{}, fmt.Errorf("take lock: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.BodyLock{}, fmt.Errorf("take lock: %w", err)
	}
	return model.BodyLock{Kind: kind, SubjectID: subjectID, Holder: holder, Session: session}, nil
}

// GetLock returns the subject's lock, or nil when it is free.
func (s *Store) GetLock(ctx context.Context, kind, subjectID string) (*model.BodyLock, error) {
	if err := checkLockKind(kind); err != nil {
		return nil, err
	}
	l := model.BodyLock{Kind: kind, SubjectID: subjectID}
	err := s.db.QueryRowContext(ctx,
		`SELECT holder FROM body_locks WHERE kind = $1 AND subject_id = $2`,
		kind, subjectID).Scan(&l.Holder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get lock: %w", err)
	}
	return &l, nil
}

// CheckInLock records a live session's check-in. An ended session gets
// *ErrLockLost; an unknown one ErrNotFound.
func (s *Store) CheckInLock(ctx context.Context, kind, subjectID string, in model.LockCheckIn) (model.BodyLock, error) {
	if err := checkLockKind(kind); err != nil {
		return model.BodyLock{}, err
	}
	l := model.BodyLock{Kind: kind, SubjectID: subjectID, Session: in.Session}
	err := s.db.QueryRowContext(ctx,
		`UPDATE body_lock_sessions
		    SET dirty = $4, last_activity = $5, checked_in_at = now()
		  WHERE id = $1 AND kind = $2 AND subject_id = $3 AND ended_at IS NULL
		  RETURNING holder`,
		in.Session, kind, subjectID, in.Dirty, in.LastActivity).Scan(&l.Holder)
	if err == nil {
		return l, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.BodyLock{}, fmt.Errorf("check in lock: %w", err)
	}
	var reason string
	var by sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT ended_reason, ended_by FROM body_lock_sessions
		  WHERE id = $1 AND kind = $2 AND subject_id = $3 AND ended_at IS NOT NULL`,
		in.Session, kind, subjectID).Scan(&reason, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return model.BodyLock{}, ErrNotFound
	}
	if err != nil {
		return model.BodyLock{}, fmt.Errorf("check in lock: %w", err)
	}
	lost := model.LockLost{Reason: reason, By: by.String, Lock: model.BodyLock{Kind: kind, SubjectID: subjectID}}
	cur, err := s.GetLock(ctx, kind, subjectID)
	if err != nil {
		return model.BodyLock{}, err
	}
	if cur != nil {
		lost.Lock = *cur
	}
	return model.BodyLock{}, &ErrLockLost{Lost: lost}
}

// ReleaseLockSession ends one live session as released. A session already
// ended or unknown is not an error.
func (s *Store) ReleaseLockSession(ctx context.Context, kind, subjectID, session string) error {
	if err := checkLockKind(kind); err != nil {
		return err
	}
	return s.endSessions(ctx, kind, subjectID, "released", "", session)
}

// UnlockBody ends every live session on the subject as unlocked by by.
func (s *Store) UnlockBody(ctx context.Context, kind, subjectID, by string) error {
	if err := checkLockKind(kind); err != nil {
		return err
	}
	return s.endSessions(ctx, kind, subjectID, "unlocked", by, "")
}

// endSessions ends the subject's live sessions (only session, when given),
// then drops the lock row if none is left.
func (s *Store) endSessions(ctx context.Context, kind, subjectID, reason, by, session string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("end lock sessions: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx,
		`UPDATE body_lock_sessions
		    SET ended_at = now(), ended_reason = $3, ended_by = NULLIF($4, '')
		  WHERE kind = $1 AND subject_id = $2 AND ended_at IS NULL AND ($5 = '' OR id = $5)`,
		kind, subjectID, reason, by, session); err != nil {
		return fmt.Errorf("end lock sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropEmptyLocks+` AND l.kind = $1 AND l.subject_id = $2`, kind, subjectID); err != nil {
		return fmt.Errorf("end lock sessions: %w", err)
	}
	return tx.Commit()
}

const dropEmptyLocks = `DELETE FROM body_locks l
 WHERE NOT EXISTS (SELECT 1 FROM body_lock_sessions s
                    WHERE s.kind = l.kind AND s.subject_id = l.subject_id AND s.ended_at IS NULL)`

// SweepLocks ends sessions with no unsaved changes idle over an hour
// (inactive) and sessions not checked in for 24 hours (expired), then drops
// locks left with no live session.
func (s *Store) SweepLocks(ctx context.Context, now time.Time) (inactive, expired int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("sweep locks: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	end := func(reason, cond string, cutoff time.Time) (int, error) {
		res, err := tx.ExecContext(ctx,
			`UPDATE body_lock_sessions SET ended_at = $1, ended_reason = '`+reason+`'
			  WHERE ended_at IS NULL AND `+cond, now, cutoff)
		if err != nil {
			return 0, fmt.Errorf("sweep locks: %w", err)
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}
	if inactive, err = end("inactive", `NOT dirty AND last_activity < $2`, now.Add(-lockIdleAfter)); err != nil {
		return 0, 0, err
	}
	if expired, err = end("expired", `checked_in_at < $2`, now.Add(-lockExpireAfter)); err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, dropEmptyLocks); err != nil {
		return 0, 0, fmt.Errorf("sweep locks: %w", err)
	}
	return inactive, expired, tx.Commit()
}
