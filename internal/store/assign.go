package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// requireActor refuses an actor id that names no row in actors. Every path
// that writes an actor into tasks.assignee checks this first: without it an
// unknown actor surfaces as a raw foreign-key violation, which poisons the
// caller's transaction instead of returning ErrNotFound.
func requireActor(tx *sql.Tx, actorID string) error {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM actors WHERE id = $1`, actorID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("actor %s: %w", actorID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("check actor %s: %w", actorID, err)
	}
	return nil
}

// lockTaskOwnership reads a task's state and assignee under a row lock, the
// opening move of every ownership change in this file. A missing task is
// ErrNotFound.
func lockTaskOwnership(tx *sql.Tx, id string) (state, assignee string, err error) {
	err = tx.QueryRow(
		`SELECT state, COALESCE(assignee, '') FROM tasks WHERE id = $1 FOR UPDATE`, id,
	).Scan(&state, &assignee)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("task %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return "", "", fmt.Errorf("lock task %s: %w", id, err)
	}
	return state, assignee, nil
}

// AssignTask sets taskID's assignee to assignee inside the given
// transaction, recording provenance via LogChange. assignee must name an
// existing actor (ErrNotFound otherwise); a missing task is also
// ErrNotFound. A task with children, or one in a closed state (see
// deliveredStateSet), cannot be assigned (ErrInvalidInput) — a container's
// ownership follows its children, and a closed task has nothing left to own.
func AssignTask(tx *sql.Tx, now time.Time, id, assignee string, eventID int64) error {
	if err := requireActor(tx, assignee); err != nil {
		return err
	}

	state, prev, err := lockTaskOwnership(tx, id)
	if err != nil {
		return err
	}
	container, err := hasChildren(tx, id)
	if err != nil {
		return err
	}
	if container {
		return fmt.Errorf("task %s has children and cannot be assigned: %w", id, ErrInvalidInput)
	}
	if deliveredStateSet[state] {
		return fmt.Errorf("task %s is %s: cannot assign: %w", id, state, ErrInvalidInput)
	}

	if _, err := tx.Exec(
		`UPDATE tasks SET assignee = $1, updated_at = $2 WHERE id = $3`,
		assignee, now.UTC(), id,
	); err != nil {
		return fmt.Errorf("assign task %s: %w", id, err)
	}
	return LogChange(tx, "task", id, eventID,
		map[string]string{"field": "assignee", "old": prev, "new": assignee})
}

// UnassignTask clears taskID's assignee inside the given transaction,
// recording provenance via LogChange. A closed task (see deliveredStateSet)
// rejects the change with ErrInvalidInput — it has nothing left to own.
// Unlike AssignTask/StartTask there is no container guard: a task with
// children is never assigned by this package, but clearing a stray assignee
// (e.g. left over from before the task took children) must not be blocked by
// the very fact that needs cleaning up. A missing task is ErrNotFound.
func UnassignTask(tx *sql.Tx, now time.Time, id string, eventID int64) error {
	state, prev, err := lockTaskOwnership(tx, id)
	if err != nil {
		return err
	}
	if deliveredStateSet[state] {
		return fmt.Errorf("task %s is %s: cannot unassign: %w", id, state, ErrInvalidInput)
	}

	if _, err := tx.Exec(
		`UPDATE tasks SET assignee = NULL, updated_at = $1 WHERE id = $2`,
		now.UTC(), id,
	); err != nil {
		return fmt.Errorf("unassign task %s: %w", id, err)
	}
	return LogChange(tx, "task", id, eventID,
		map[string]string{"field": "assignee", "old": prev, "new": ""})
}

// StartTask moves taskID from ready to in_progress on behalf of actorID
// without taking a lease: a human owns the task by assignment instead.
// actorID must name an existing actor (ErrNotFound otherwise, the same check
// AssignTask makes — without it an unknown actor would surface as a raw
// tasks.assignee foreign-key violation and poison the caller's transaction).
// If the task is unassigned, actorID is assigned to it first (recorded via
// LogChange); if it is already assigned to someone else, StartTask refuses
// with ErrInvalidInput rather than silently reassigning. A task with
// children, a closed task (see deliveredStateSet), or a blocked task are rejected the same way
// (ErrInvalidInput) — see IsBlocked and Claim for the equivalent lease-based
// guards. A missing task is ErrNotFound. Returns the assignee the task
// settled on (actorID, whether it was already assigned or just
// auto-assigned here), so a caller that auto-assigned can tell.
func StartTask(tx *sql.Tx, now time.Time, id, actorID string, eventID int64) (string, error) {
	if err := requireActor(tx, actorID); err != nil {
		return "", err
	}

	state, assignee, err := lockTaskOwnership(tx, id)
	if err != nil {
		return "", err
	}
	container, err := hasChildren(tx, id)
	if err != nil {
		return "", err
	}
	if container {
		return "", fmt.Errorf("task %s has children and cannot be started: %w", id, ErrInvalidInput)
	}
	if deliveredStateSet[state] {
		return "", fmt.Errorf("task %s is %s: cannot start: %w", id, state, ErrInvalidInput)
	}
	if assignee != "" && assignee != actorID {
		return "", fmt.Errorf("task %s: assigned to %s; unassign first: %w", id, assignee, ErrInvalidInput)
	}

	blocked, err := IsBlocked(tx, id)
	if err != nil {
		return "", err
	}
	if blocked {
		return "", fmt.Errorf("task %s is blocked: %w", id, ErrInvalidInput)
	}

	if assignee == "" {
		if _, err := tx.Exec(
			`UPDATE tasks SET assignee = $1, updated_at = $2 WHERE id = $3`,
			actorID, now.UTC(), id,
		); err != nil {
			return "", fmt.Errorf("assign task %s: %w", id, err)
		}
		// old is always "" here: this branch only runs when the task was
		// unassigned.
		if err := LogChange(tx, "task", id, eventID,
			map[string]string{"field": "assignee", "old": "", "new": actorID}); err != nil {
			return "", err
		}
		assignee = actorID
	}

	// state came from lockTaskOwnership's FOR UPDATE read, so the transition
	// needs no re-read.
	if err := transitionKnown(tx, now, id, state, "ready", "in_progress", eventID); err != nil {
		return "", err
	}
	return assignee, nil
}

// StopTask moves taskID from in_progress back to ready on behalf of actorID,
// the assignment-based counterpart to Release. actorID must be the task's
// current assignee and the task must be in_progress, or StopTask refuses
// with ErrInvalidInput (the two conditions are checked and reported
// separately, so the error names the actual state/assignee rather than just
// echoing the caller). A task with an active lease must be released through
// Release instead (ErrInvalidInput) — StopTask never touches a lease. A
// missing task is ErrNotFound.
func StopTask(tx *sql.Tx, now time.Time, id, actorID string, eventID int64) error {
	state, assignee, err := lockTaskOwnership(tx, id)
	if err != nil {
		return err
	}
	if state != "in_progress" {
		return fmt.Errorf("task %s is %s, not in_progress: %w", id, state, ErrInvalidInput)
	}
	if assignee != actorID {
		return fmt.Errorf("task %s is assigned to %q, not %s: %w", id, assignee, actorID, ErrInvalidInput)
	}

	leased, err := hasActiveLease(tx, id)
	if err != nil {
		return err
	}
	if leased {
		return fmt.Errorf("task %s: held by an active lease; use release: %w", id, ErrInvalidInput)
	}

	return transitionKnown(tx, now, id, state, "in_progress", "ready", eventID)
}
