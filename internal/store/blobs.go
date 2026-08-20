package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// InsertBlob records a blob, returning the existing row unchanged if the
// hash is already known. Idempotent by construction: identical bytes hash
// identically, so a re-upload must not create a second row or restamp the
// first -- created_at is the orphan sweep's grace-period clock.
func (s *Store) InsertBlob(ctx context.Context, hash, mediaType string, size int64) (model.Blob, error) {
	var b model.Blob
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO blobs (hash, media_type, size, created_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (hash) DO UPDATE SET hash = EXCLUDED.hash
		 RETURNING hash, media_type, size, created_at`,
		hash, mediaType, size, s.nowFn().UTC(),
	).Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt)
	if err != nil {
		return model.Blob{}, fmt.Errorf("insert blob: %w", err)
	}
	return b, nil
}

// GetBlob returns one blob by hash, or ErrNotFound.
func (s *Store) GetBlob(ctx context.Context, hash string) (model.Blob, error) {
	var b model.Blob
	err := s.db.QueryRowContext(ctx,
		`SELECT hash, media_type, size, created_at FROM blobs WHERE hash = $1`, hash,
	).Scan(&b.Hash, &b.MediaType, &b.Size, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Blob{}, ErrNotFound
	}
	if err != nil {
		return model.Blob{}, fmt.Errorf("get blob: %w", err)
	}
	return b, nil
}

// ReconcileEmbedded makes the task's embedded references exactly hashes.
// Runs inside the same transaction as the task write, so the flag can never
// disagree with the body that produced it.
//
// A row the body stopped citing goes two ways: deleted if nothing else
// references it (an embedded-only row would otherwise become neither
// embedded nor attached, which the task_blobs_referenced CHECK rejects),
// or just loses its derived flag if `lode task attach` also declared it.
func ReconcileEmbedded(tx *sql.Tx, now time.Time, taskID string, hashes []string, actorID string) error {
	// A nil slice encodes as SQL NULL, and `hash = ANY(NULL)` is NULL rather
	// than false, so an empty body would match nothing and prune nothing.
	if hashes == nil {
		hashes = []string{}
	}

	if _, err := tx.Exec(
		`DELETE FROM task_blobs
		  WHERE task_id = $1 AND embedded AND NOT attached AND NOT (hash = ANY($2))`,
		taskID, hashes); err != nil {
		return fmt.Errorf("prune dropped embeds: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE task_blobs SET embedded = false
		  WHERE task_id = $1 AND embedded AND attached AND NOT (hash = ANY($2))`,
		taskID, hashes); err != nil {
		return fmt.Errorf("clear embedded: %w", err)
	}

	for _, h := range hashes {
		if _, err := tx.Exec(
			`INSERT INTO task_blobs (task_id, hash, filename, embedded, created_by, created_at)
			 VALUES ($1, $2, '', true, $3, $4)
			 ON CONFLICT (task_id, hash) DO UPDATE SET embedded = true`,
			taskID, h, nullString(actorID), now.UTC()); err != nil {
			if pgViolation(err, "23503", "task_blobs_hash_fkey") {
				return fmt.Errorf("%w: %s", ErrUnknownBlob, h)
			}
			return fmt.Errorf("set embedded %s: %w", h, err)
		}
	}

	return nil
}

// TaskBody reads a task's body inside a caller's transaction. Reconciliation
// needs the body as stored, not as patched: a PATCH that leaves the body
// alone must still reconcile against the body that is actually there. body
// is nullable (a task created without one), so an unset body reconciles as
// if it cited nothing.
func TaskBody(tx *sql.Tx, taskID string) (string, error) {
	var body sql.NullString
	if err := tx.QueryRow(`SELECT body FROM tasks WHERE id = $1`, taskID).Scan(&body); err != nil {
		return "", fmt.Errorf("read task body: %w", err)
	}
	return body.String, nil
}

// AttachBlob records an explicit, non-body reference. filename is optional,
// so an attach that omits it keeps the name an earlier one recorded rather
// than blanking it -- and a row the body embedded carries ” until an attach
// names it.
func (s *Store) AttachBlob(ctx context.Context, taskID, hash, filename, actorID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_blobs (task_id, hash, filename, attached, created_by, created_at)
		 VALUES ($1, $2, $3, true, $4, $5)
		 ON CONFLICT (task_id, hash)
		 DO UPDATE SET attached = true,
		               filename = COALESCE(NULLIF(EXCLUDED.filename, ''), task_blobs.filename)`,
		taskID, hash, filename, nullString(actorID), s.nowFn().UTC())
	if err != nil {
		if pgViolation(err, "23503", "task_blobs_hash_fkey") {
			return fmt.Errorf("%w: %s", ErrUnknownBlob, hash)
		}
		return fmt.Errorf("attach blob: %w", err)
	}
	return nil
}

// DetachBlob clears the explicit reference, deleting the row unless the body
// still embeds the blob.
//
// Same interlock as ReconcileEmbedded: an attached-only row must be deleted,
// not merely cleared, or task_blobs_referenced rejects it. A row the body
// still embeds just loses its declared half.
//
// Which of the two applies is decided under FOR UPDATE, not by putting the
// flag in each statement's WHERE. Read committed re-plans every statement
// against a fresh snapshot, so a concurrent body edit landing between a
// delete guarded by `NOT embedded` and an update guarded by `embedded` makes
// the row match neither -- the detach vanishes and the blob stays pinned
// against GC. Locking the row first makes that edit wait its turn.
func (s *Store) DetachBlob(ctx context.Context, taskID, hash string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var embedded bool
		err := tx.QueryRowContext(ctx,
			`SELECT embedded FROM task_blobs
			  WHERE task_id = $1 AND hash = $2 AND attached FOR UPDATE`,
			taskID, hash).Scan(&embedded)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock task blob: %w", err)
		}
		stmt := `DELETE FROM task_blobs WHERE task_id = $1 AND hash = $2`
		if embedded {
			stmt = `UPDATE task_blobs SET attached = false WHERE task_id = $1 AND hash = $2`
		}
		if _, err := tx.ExecContext(ctx, stmt, taskID, hash); err != nil {
			return fmt.Errorf("detach blob: %w", err)
		}
		return nil
	})
}

// ListTaskBlobs returns a task's references joined to their blobs, embedded
// first, then by filename, for a stable display order.
func (s *Store) ListTaskBlobs(ctx context.Context, taskID string) ([]model.TaskBlob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tb.hash, tb.filename, b.media_type, b.size, tb.embedded, tb.attached
		   FROM task_blobs tb JOIN blobs b ON b.hash = tb.hash
		  WHERE tb.task_id = $1
		  ORDER BY tb.embedded DESC, tb.filename, tb.hash`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task blobs: %w", err)
	}
	defer rows.Close()
	var out []model.TaskBlob
	for rows.Next() {
		var tb model.TaskBlob
		if err := rows.Scan(&tb.Hash, &tb.Filename, &tb.MediaType, &tb.Size,
			&tb.Embedded, &tb.Attached); err != nil {
			return nil, fmt.Errorf("scan task blob: %w", err)
		}
		out = append(out, tb)
	}
	return out, rows.Err()
}

// nullString maps "" to a NULL created_by, since the column references
// actors(id) and an empty string is not an actor.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
