package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// SetChecklistItem checks or unchecks one checklist item in the task's body,
// identified by input.Ordinal (canonical) or input.Title, exactly one of
// which must be set. It locks the task row first (SELECT ... FOR UPDATE, the
// same pattern Claim uses) so two concurrent calls on the same task never
// clobber each other. Returns ErrNotFound if the task does not exist, and
// ErrInvalidInput if the identifier is missing/ambiguous or names no item.
func SetChecklistItem(tx *sql.Tx, now time.Time, id string, input model.SetChecklistItemInput) (model.ChecklistItem, error) {
	if (input.Ordinal == nil) == (input.Title == nil) {
		return model.ChecklistItem{}, fmt.Errorf("exactly one of ordinal or title must be set: %w", ErrInvalidInput)
	}

	var body sql.NullString
	if err := tx.QueryRow(
		`SELECT body FROM tasks WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id,
	).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ChecklistItem{}, fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		return model.ChecklistItem{}, fmt.Errorf("lock task %s: %w", id, err)
	}

	items := model.ParseChecklist(body.String)
	ordinal, err := resolveChecklistOrdinal(items, input)
	if err != nil {
		return model.ChecklistItem{}, err
	}

	newBody, item, ok := model.SetChecklistMark(body.String, ordinal, input.Checked)
	if !ok {
		return model.ChecklistItem{}, fmt.Errorf("checklist ordinal %d: %w", ordinal, ErrInvalidInput)
	}

	if _, err := tx.Exec(`UPDATE tasks SET body = $2, updated_at = $3 WHERE id = $1`,
		id, newBody, now.UTC()); err != nil {
		return model.ChecklistItem{}, fmt.Errorf("set checklist item on task %s: %w", id, err)
	}
	return item, nil
}

// resolveChecklistOrdinal turns input's ordinal-or-title identifier into a
// concrete ordinal against items, the checklist already parsed from the
// task's current body.
func resolveChecklistOrdinal(items []model.ChecklistItem, input model.SetChecklistItemInput) (int, error) {
	if input.Ordinal != nil {
		ord := *input.Ordinal
		if ord < 0 || ord >= len(items) {
			return 0, fmt.Errorf("checklist ordinal %d out of range (0..%d): %w", ord, len(items)-1, ErrInvalidInput)
		}
		return ord, nil
	}

	title := *input.Title
	match := -1
	for _, it := range items {
		if it.Title != title {
			continue
		}
		if match != -1 {
			return 0, fmt.Errorf("checklist title %q is ambiguous, use ordinal instead: %w", title, ErrInvalidInput)
		}
		match = it.Ordinal
	}
	if match == -1 {
		return 0, fmt.Errorf("no checklist item titled %q: %w", title, ErrInvalidInput)
	}
	return match, nil
}
