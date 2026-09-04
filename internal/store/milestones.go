// milestones.go implements spec 029 §2's milestone: one ordered container in
// a project, holding tasks and deliverables. A milestone stores identity,
// title and ordering only — its progress is a query over its children
// (milestone_progress.go), never a column.
package store

import (
	"context"
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

// scanMilestone reads one row selected with milestoneColumns. Progress is not
// in the row — it is derived by the callers below and by nothing else.
func scanMilestone(row rowScanner) (*model.Milestone, error) {
	var m model.Milestone
	var createdBy sql.NullString
	if err := row.Scan(&m.ID, &m.Project, &m.Title, &m.Position,
		&createdBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.CreatedBy = createdBy.String
	m.CreatedAt = m.CreatedAt.UTC()
	m.UpdatedAt = m.UpdatedAt.UTC()
	return &m, nil
}

// ListMilestones returns a project's milestones ordered by position then id,
// each with derived progress. An unknown project yields an empty slice, not
// an error — callers that need the project to exist load it first, as
// ListDeliverables' callers do.
//
// The children come back in two bulk queries, one per kind, grouped in Go:
// the alternative is a query per milestone, and a milestone's progress is
// only ever read as part of a list.
func (s *Store) ListMilestones(ctx context.Context, projectID string) ([]model.Milestone, error) {
	what := "list milestones for " + projectID
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+milestoneColumns+` FROM milestones WHERE project_id = $1
		 ORDER BY position, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	out, err := collectRows(rows, what, byValue(scanMilestone))
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []model.Milestone{}, nil
	}

	taskStates, err := s.milestoneTaskStates(ctx, projectID)
	if err != nil {
		return nil, err
	}
	deliverableStates, err := s.milestoneDeliverableStates(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Progress = ComputeMilestoneProgress(
			taskStates[out[i].ID], deliverableStates[out[i].ID])
	}
	return out, nil
}

// milestoneTaskStates reads the states of every attached task in a project,
// keyed by milestone. A tombstoned task is out of the counts for the reason
// it is out of every listing (044 §4).
func (s *Store) milestoneTaskStates(ctx context.Context, projectID string) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT milestone_id, state FROM tasks
		  WHERE project_id = $1 AND milestone_id IS NOT NULL AND deleted_at IS NULL`, projectID)
	if err != nil {
		return nil, fmt.Errorf("milestone task states for %s: %w", projectID, err)
	}
	return groupRows(rows, "milestone task states for "+projectID,
		func(r rowScanner) (string, string, error) {
			var milestoneID, state string
			err := r.Scan(&milestoneID, &state)
			return milestoneID, state, err
		})
}

// milestoneDeliverableStates reads the reported state of every attached
// deliverable in a project, keyed by milestone. It goes through the
// deliverable projection rather than joining artifact_evidence itself, so
// "the newest fact reported about the declared address" has one spelling.
func (s *Store) milestoneDeliverableStates(ctx context.Context, projectID string) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deliverableSelect+`, milestone_id `+deliverableFrom+`
		  WHERE project_id = $1 AND milestone_id IS NOT NULL`, projectID)
	if err != nil {
		return nil, fmt.Errorf("milestone deliverable states for %s: %w", projectID, err)
	}
	return groupRows(rows, "milestone deliverable states for "+projectID,
		func(r rowScanner) (string, string, error) {
			var milestoneID string
			d, err := scanDeliverable(appendScan{r, []any{&milestoneID}})
			if err != nil {
				return "", "", err
			}
			return milestoneID, d.ReportedState, nil
		})
}

// GetMilestone returns one milestone with its tasks, its deliverables, and
// the progress derived from exactly those children. ErrNotFound when the id
// names no milestone.
func (s *Store) GetMilestone(ctx context.Context, id string) (*model.MilestoneDetail, error) {
	m, err := scanMilestone(s.db.QueryRowContext(ctx,
		`SELECT `+milestoneColumns+` FROM milestones WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get milestone %s: %w", id, err)
	}
	detail := &model.MilestoneDetail{Milestone: *m}

	if detail.Tasks, err = s.milestoneTasks(ctx, id); err != nil {
		return nil, err
	}
	what := "deliverables of milestone " + id
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deliverableSelect+` `+deliverableFrom+`
		  WHERE milestone_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if detail.Deliverables, err = collectRows(rows, what, byValue(scanDeliverable)); err != nil {
		return nil, err
	}

	taskStates := make([]string, len(detail.Tasks))
	for i, t := range detail.Tasks {
		taskStates[i] = t.State
	}
	deliverableStates := make([]string, len(detail.Deliverables))
	for i, d := range detail.Deliverables {
		deliverableStates[i] = d.ReportedState
	}
	detail.Progress = ComputeMilestoneProgress(taskStates, deliverableStates)
	detail.Tasks = nonNil(detail.Tasks)
	detail.Deliverables = nonNil(detail.Deliverables)
	return detail, nil
}

// milestoneTasks lists a milestone's live tasks in ListTasks' order, with the
// same derived closedness — the detail's task list has to look like every
// other task list.
func (s *Store) milestoneTasks(ctx context.Context, id string) ([]model.Task, error) {
	what := "tasks of milestone " + id
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+` FROM tasks
		  WHERE milestone_id = $1 AND deleted_at IS NULL`+taskListOrder("tasks"), id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	out, err := collectRows(rows, what, byValue(scanTask))
	if err != nil {
		return nil, err
	}
	// A second query for the reason ListTasks gives: taskClosed binds aliases
	// that would collide with the column list above.
	ids := make([]string, len(out))
	for i, t := range out {
		ids[i] = t.ID
	}
	closed, err := s.ClosedTaskIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	for i := range out {
		out[i].Closed = closed[out[i].ID]
	}
	return out, nil
}

// ListMilestoneChildren returns every attached task and deliverable in a
// project, grouped by milestone id, in the same order and with the same
// derived fields their own listings carry. Work attached to no milestone is
// grouped nowhere, and an unknown project yields empty maps.
//
// This is the Milestones page's reader: one page shows every milestone with
// its children, and calling GetMilestone once per milestone would be a query
// per section. The shape matches ListMilestones' own — two bulk queries, one
// per kind, grouped in Go.
func (s *Store) ListMilestoneChildren(ctx context.Context, projectID string) (map[string][]model.Task, map[string][]model.Deliverable, error) {
	what := "milestone tasks for " + projectID
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+taskColumns+`, milestone_id FROM tasks
		  WHERE project_id = $1 AND milestone_id IS NOT NULL AND deleted_at IS NULL`+
			taskListOrder("tasks"), projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", what, err)
	}
	tasks, err := groupRows(rows, what, func(r rowScanner) (string, model.Task, error) {
		var milestoneID string
		t, err := scanTask(appendScan{r, []any{&milestoneID}})
		if err != nil {
			return "", model.Task{}, err
		}
		return milestoneID, *t, nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Closedness is derived, not stored, and taskClosed binds aliases that
	// would collide with the column list above — the same second query
	// ListTasks and milestoneTasks each run.
	var ids []string
	for _, list := range tasks {
		for _, t := range list {
			ids = append(ids, t.ID)
		}
	}
	closed, err := s.ClosedTaskIDs(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", what, err)
	}
	for _, list := range tasks {
		for i := range list {
			list[i].Closed = closed[list[i].ID]
		}
	}

	what = "milestone deliverables for " + projectID
	rows, err = s.db.QueryContext(ctx,
		`SELECT `+deliverableSelect+`, milestone_id `+deliverableFrom+`
		  WHERE project_id = $1 AND milestone_id IS NOT NULL
		  ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", what, err)
	}
	deliverables, err := groupRows(rows, what, func(r rowScanner) (string, model.Deliverable, error) {
		var milestoneID string
		d, err := scanDeliverable(appendScan{r, []any{&milestoneID}})
		if err != nil {
			return "", model.Deliverable{}, err
		}
		return milestoneID, *d, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return tasks, deliverables, nil
}
