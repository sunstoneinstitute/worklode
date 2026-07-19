package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Issue is one GitHub issue tracked in the inbox: an unfiltered feed of
// issues.opened/closed webhooks, triaged by a human into either a task
// (promoted) or nothing (dismissed).
type Issue struct {
	Repo              string
	Number            int64
	Title             string
	State             string
	TriageState       string
	TaskID            *string
	AppliesToVersions []string
	URL               string
}

// UpsertIssue inserts a new inbox row, or on redelivery updates only
// title/state/url. It never touches triage_state, task_id, or
// applies_to_versions — those are set once, by triage (PromoteIssue /
// DismissIssue), and a later webhook replay must not clobber them.
func UpsertIssue(tx *sql.Tx, is Issue) error {
	_, err := tx.Exec(
		`INSERT INTO issues (repo, number, title, state, url)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (repo, number) DO UPDATE SET
		   title = excluded.title,
		   state = excluded.state,
		   url = excluded.url`,
		is.Repo, is.Number, is.Title, is.State, is.URL,
	)
	if err != nil {
		return fmt.Errorf("upsert issue %s#%d: %w", is.Repo, is.Number, err)
	}
	return nil
}

// PromoteIssue turns an inbox issue into a task: it creates the task via
// CreateTask, then marks the issue triage_state=promoted, linking task_id and
// recording appliesToVersions (marshalled to JSON). The issue must currently
// be triage_state='new' — anything else (already promoted, dismissed, or no
// such issue) is an error.
func PromoteIssue(tx *sql.Tx, now time.Time, repo string, number int64, in TaskInput, appliesToVersions []string) (*Task, error) {
	var triageState string
	err := tx.QueryRow(
		`SELECT triage_state FROM issues WHERE repo = ? AND number = ?`, repo, number,
	).Scan(&triageState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("issue %s#%d: %w", repo, number, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get issue %s#%d triage_state: %w", repo, number, err)
	}
	if triageState != "new" {
		return nil, fmt.Errorf("issue %s#%d is %s, not new: %w", repo, number, triageState, ErrBadTransition)
	}

	task, err := CreateTask(tx, now, in)
	if err != nil {
		return nil, err
	}

	versionsJSON, err := json.Marshal(appliesToVersions)
	if err != nil {
		return nil, fmt.Errorf("marshal applies_to_versions: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE issues SET triage_state = 'promoted', task_id = ?, applies_to_versions = ?
		 WHERE repo = ? AND number = ?`,
		task.ID, string(versionsJSON), repo, number,
	); err != nil {
		return nil, fmt.Errorf("promote issue %s#%d: %w", repo, number, err)
	}
	return task, nil
}

// DismissIssue marks an inbox issue triage_state=dismissed. The issue must
// currently be triage_state='new'. Returns ErrNotFound if no such issue
// exists.
func DismissIssue(tx *sql.Tx, repo string, number int64) error {
	var triageState string
	err := tx.QueryRow(
		`SELECT triage_state FROM issues WHERE repo = ? AND number = ?`, repo, number,
	).Scan(&triageState)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("issue %s#%d: %w", repo, number, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get issue %s#%d triage_state: %w", repo, number, err)
	}
	if triageState != "new" {
		return fmt.Errorf("issue %s#%d is %s, not new: %w", repo, number, triageState, ErrBadTransition)
	}
	if _, err := tx.Exec(
		`UPDATE issues SET triage_state = 'dismissed' WHERE repo = ? AND number = ?`,
		repo, number,
	); err != nil {
		return fmt.Errorf("dismiss issue %s#%d: %w", repo, number, err)
	}
	return nil
}

func scanIssue(row rowScanner) (*Issue, error) {
	var is Issue
	var title, state, taskID, appliesToVersions, url sql.NullString
	if err := row.Scan(&is.Repo, &is.Number, &title, &state, &is.TriageState,
		&taskID, &appliesToVersions, &url); err != nil {
		return nil, err
	}
	is.Title = title.String
	is.State = state.String
	is.URL = url.String
	if taskID.Valid {
		is.TaskID = &taskID.String
	}
	if appliesToVersions.Valid {
		if err := json.Unmarshal([]byte(appliesToVersions.String), &is.AppliesToVersions); err != nil {
			return nil, fmt.Errorf("unmarshal applies_to_versions for issue %s#%d: %w", is.Repo, is.Number, err)
		}
	}
	return &is, nil
}

// ListIssues returns inbox issues, optionally filtered by triage_state
// ("" means all), ordered by repo then number.
func (s *Store) ListIssues(ctx context.Context, triageState string) ([]Issue, error) {
	q := `SELECT repo, number, title, state, triage_state, task_id, applies_to_versions, url FROM issues`
	var args []any
	if triageState != "" {
		q += ` WHERE triage_state = ?`
		args = append(args, triageState)
	}
	q += ` ORDER BY repo, number`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()

	var out []Issue
	for rows.Next() {
		is, err := scanIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("scan issue: %w", err)
		}
		out = append(out, *is)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	return out, nil
}
