// milestones.go implements spec 029 §2's milestone: one ordered container in
// a project, holding tasks and deliverables. A milestone stores identity,
// title and ordering only — its progress is a query over its children
// (milestone_progress.go), never a column.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// milestoneSeqKind is the milestone's row key in project_entity_seq and the
// type segment of its id (spec 029 §4's COW-MILE-2).
const milestoneSeqKind = "MILE"

// maxMilestoneTitle bounds a title in runes, matching a deliverable's name.
// It keeps a stray paste out of the row and out of a list cell; 029 §2 puts
// no length on the field itself.
const maxMilestoneTitle = 200

// milestoneColumns is the milestones table's column list, in insert and scan
// order.
const milestoneColumns = `id, project_id, title, position, created_by, created_at, updated_at`

// CreateMilestone allocates the next <KEY>-MILE-<n> id from the project's
// MILE counter and inserts the milestone inside the given transaction. Like
// CreateDeliverable it is meant to be called from a RecordEvent apply
// callback with the store's clock as now. position 0 appends after the
// project's current last position. A blank or over-long title is
// ErrInvalidInput and an unknown project ErrNotFound, both checked before
// the id is allocated so a rejected input never burns an ordinal.
func CreateMilestone(tx *sql.Tx, now time.Time, projectID, title string, position int, createdBy string) (*model.Milestone, error) {
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		return nil, fmt.Errorf("milestone title is empty: %w", ErrInvalidInput)
	case utf8.RuneCountInString(title) > maxMilestoneTitle:
		return nil, fmt.Errorf("milestone title is too long: %w", ErrInvalidInput)
	case position < 0:
		return nil, fmt.Errorf("milestone position is negative: %w", ErrInvalidInput)
	}

	var key string
	if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, projectID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("project %s: %w", projectID, ErrNotFound)
		}
		return nil, fmt.Errorf("look up project %s: %w", projectID, err)
	}

	if position == 0 {
		// Resolved in the same transaction as the insert, so two concurrent
		// appends cannot both read the same last position.
		if err := tx.QueryRow(
			`SELECT COALESCE(MAX(position), 0) + 1 FROM milestones WHERE project_id = $1`,
			projectID,
		).Scan(&position); err != nil {
			return nil, fmt.Errorf("resolve milestone position for %s: %w", projectID, err)
		}
	}

	// The upsert both creates the counter row on a project's first milestone
	// (next = 2, ordinal 1) and advances it afterwards, holding the row lock
	// for the rest of the transaction so two concurrent creates cannot draw
	// the same ordinal.
	var n int64
	if err := tx.QueryRow(
		`INSERT INTO project_entity_seq (project_id, kind, next) VALUES ($1, $2, 2)
		 ON CONFLICT (project_id, kind) DO UPDATE SET next = project_entity_seq.next + 1
		 RETURNING next - 1`,
		projectID, milestoneSeqKind,
	).Scan(&n); err != nil {
		return nil, fmt.Errorf("allocate milestone id: %w", err)
	}

	ts := now.UTC().Truncate(time.Second)
	var creator sql.NullString
	if createdBy != "" {
		creator = sql.NullString{String: createdBy, Valid: true}
	}
	m := &model.Milestone{
		ID:        fmt.Sprintf("%s-%s-%d", key, milestoneSeqKind, n),
		Project:   projectID,
		Title:     title,
		Position:  position,
		CreatedBy: createdBy,
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	if _, err := tx.Exec(
		`INSERT INTO milestones (`+milestoneColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		m.ID, m.Project, m.Title, m.Position, creator, m.CreatedAt, m.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert milestone %s: %w", m.ID, err)
	}
	return m, nil
}
