package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Soft delete for tasks and documents (spec 044).
//
// Delete tombstones a row rather than removing it: the events log is
// append-only, and every event, state_log row, edge and artifact naming a task
// or document has to stay valid. `deleted_at IS NULL` is the whole predicate
// for "live" — deleted_by and delete_justification are payload the tombstone
// carries and are never filtered on.
//
// Whether a justification is required is not decided here. That rule keys off
// the instance environment (039 §3), which only the API layer knows; the store
// records whatever it is given.

// tombstoneFrom builds the model tombstone from the three columns. The columns
// are all-null or all-set together, so deleted_at alone decides.
func tombstoneFrom(deletedAt sql.NullTime, deletedBy, justification sql.NullString) *model.Tombstone {
	if !deletedAt.Valid {
		return nil
	}
	return &model.Tombstone{
		DeletedAt:     deletedAt.Time.UTC(),
		DeletedBy:     deletedBy.String,
		Justification: justification.String,
	}
}

// lockTaskTombstone reads a task's tombstone flag and state under a row lock,
// so two deletes of one task serialise rather than racing. A missing task is
// ErrNotFound.
func lockTaskTombstone(tx *sql.Tx, id string) (deleted bool, state string, err error) {
	var deletedAt sql.NullTime
	err = tx.QueryRow(
		`SELECT deleted_at, state FROM tasks WHERE id = $1 FOR UPDATE`, id).Scan(&deletedAt, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return false, "", fmt.Errorf("lock task %s: %w", id, err)
	}
	return deletedAt.Valid, state, nil
}

// lockDocTombstone is lockTaskTombstone for a document.
func lockDocTombstone(tx *sql.Tx, id int64) (deleted bool, err error) {
	var deletedAt sql.NullTime
	err = tx.QueryRow(`SELECT deleted_at FROM docs WHERE id = $1 FOR UPDATE`, id).Scan(&deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("doc %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return false, fmt.Errorf("lock doc %d: %w", id, err)
	}
	return deletedAt.Valid, nil
}

// DeleteTask tombstones a task inside the given transaction, closes any active
// lease on it, and appends a state_log row attributed to eventID. Call it from
// a RecordEvent apply callback with the store's clock as now.
//
// actorID must name an existing actor. justification is stored verbatim and may
// be empty — 044 §3's requirement is the API layer's to enforce. Deleting an
// already-deleted task is ErrInvalidInput, not a silent success: the caller
// asked to hide a row that is already hidden, and the tombstone it would
// overwrite names someone else.
//
// The delete does not cascade (044 §2). Children, covering plans and edges keep
// naming a row that still exists.
func DeleteTask(tx *sql.Tx, now time.Time, id, actorID, justification string, eventID int64) error {
	if err := requireActor(tx, actorID); err != nil {
		return err
	}
	deleted, state, err := lockTaskTombstone(tx, id)
	if err != nil {
		return err
	}
	if deleted {
		return fmt.Errorf("task %s is already deleted: %w", id, ErrInvalidInput)
	}
	if _, err := tx.Exec(
		`UPDATE tasks SET deleted_at = $1, deleted_by = $2, delete_justification = $3, updated_at = $1
		  WHERE id = $4`,
		now.UTC(), actorID, nullText(justification), id,
	); err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	// A hidden task cannot be worked, and leaving the lease would leave the
	// sweeper tending a row nothing can see (044 §2).
	if err := CloseActiveLease(tx, now, id); err != nil {
		return err
	}
	if err := LogChange(tx, "task", id, eventID, map[string]string{
		"field": "deleted", "old": "false", "new": "true", "justification": justification,
	}); err != nil {
		return err
	}
	// CloseActiveLease never touches task state — its callers do. Without this
	// an in_progress task would be left with no lease and no sweeper watching
	// it, so undelete would hand back a row Claim refuses forever. Same move
	// closeLease makes, and it must precede the roll-up below so the parent
	// resolves against the state the child actually ends on.
	if state == "in_progress" {
		// state came from lockTaskTombstone's FOR UPDATE read; nothing since
		// has moved it, so the transition needs no re-read.
		if err := transitionKnown(tx, now, id, state, "in_progress", "ready", eventID); err != nil {
			return err
		}
	}
	// The child just left its parent's roll-up (childStates reads live children
	// only), and only Transition otherwise re-runs the resolver — so deleting
	// the last unfinished child would leave the parent where that child put it
	// until some unrelated transition happened along.
	return resolveParent(tx, now, id, eventID)
}

// UndeleteTask clears a task's tombstone inside the given transaction and
// appends a state_log row attributed to eventID. Undeleting a task that is not
// deleted is ErrInvalidInput. No justification is required on either instance
// (044 §3): deleting hides the record, undeleting restores it, and only the
// first is worth making someone stop and type.
func UndeleteTask(tx *sql.Tx, now time.Time, id string, eventID int64) error {
	deleted, _, err := lockTaskTombstone(tx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return fmt.Errorf("task %s is not deleted: %w", id, ErrInvalidInput)
	}
	if _, err := tx.Exec(
		`UPDATE tasks SET deleted_at = NULL, deleted_by = NULL, delete_justification = NULL,
		        updated_at = $1 WHERE id = $2`, now.UTC(), id,
	); err != nil {
		return fmt.Errorf("undelete task %s: %w", id, err)
	}
	if err := LogChange(tx, "task", id, eventID,
		map[string]string{"field": "deleted", "old": "true", "new": "false"}); err != nil {
		return err
	}
	// The parent's roll-up gains a child back, which is the mirror of the
	// re-resolve DeleteTask does: a parent closed while this child was hidden
	// has to reopen.
	return resolveParent(tx, now, id, eventID)
}

// DeleteDoc tombstones a document inside the given transaction and appends a
// state_log row attributed to eventID. See DeleteTask for the semantics; a
// document holds no lease, so there is nothing to close.
func DeleteDoc(tx *sql.Tx, now time.Time, id int64, actorID, justification string, eventID int64) error {
	if err := requireActor(tx, actorID); err != nil {
		return err
	}
	deleted, err := lockDocTombstone(tx, id)
	if err != nil {
		return err
	}
	if deleted {
		return fmt.Errorf("doc %d is already deleted: %w", id, ErrInvalidInput)
	}
	if _, err := tx.Exec(
		`UPDATE docs SET deleted_at = $1, deleted_by = $2, delete_justification = $3, updated_at = $1
		  WHERE id = $4`,
		now.UTC(), actorID, nullText(justification), id,
	); err != nil {
		return fmt.Errorf("delete doc %d: %w", id, err)
	}
	return logDocChange(tx, id, eventID, map[string]string{
		"field": "deleted", "old": "false", "new": "true", "justification": justification,
	})
}

// UndeleteDoc clears a document's tombstone inside the given transaction. See
// UndeleteTask.
func UndeleteDoc(tx *sql.Tx, now time.Time, id int64, eventID int64) error {
	deleted, err := lockDocTombstone(tx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return fmt.Errorf("doc %d is not deleted: %w", id, ErrInvalidInput)
	}
	if _, err := tx.Exec(
		`UPDATE docs SET deleted_at = NULL, deleted_by = NULL, delete_justification = NULL,
		        updated_at = $1 WHERE id = $2`, now.UTC(), id,
	); err != nil {
		return fmt.Errorf("undelete doc %d: %w", id, err)
	}
	return logDocChange(tx, id, eventID,
		map[string]string{"field": "deleted", "old": "true", "new": "false"})
}
