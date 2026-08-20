package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// UpsertIssue inserts a new inbox row, or on redelivery updates only
// title/state/url. It never touches triage_state, task_id, or
// applies_to_versions — those are set once, by triage (PromoteIssue /
// DismissIssue), and a later webhook replay must not clobber them.
//
// updatedAt is GitHub's issue.updated_at; the update is guarded on it so a
// stale delivery cannot regress title/state/url — see UpsertPR for the
// guard's rationale. It is a parameter rather than a model.Issue field
// because the fact timestamp is an ingestion concern, not a wire field.
func UpsertIssue(tx *sql.Tx, is model.Issue, updatedAt time.Time) error {
	var updated sql.NullTime
	if !updatedAt.IsZero() {
		updated = sql.NullTime{Time: updatedAt.UTC(), Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO issues (repo, number, title, state, url, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (repo, number) DO UPDATE SET
		   title = excluded.title,
		   state = excluded.state,
		   url = excluded.url,
		   updated_at = excluded.updated_at
		 WHERE coalesce(excluded.updated_at, '-infinity') >= coalesce(issues.updated_at, '-infinity')`,
		is.Repo, is.Number, is.Title, is.State, is.URL, updated,
	)
	if err != nil {
		return fmt.Errorf("upsert issue %s#%d: %w", is.Repo, is.Number, err)
	}
	return nil
}

// ExistingIssueNumbers returns the inbox issue numbers already stored for
// repo. Import reads it before upserting so it can report new rows separately
// from updated ones — the upsert itself cannot distinguish the two, and a
// dry run must report the same split without writing.
func ExistingIssueNumbers(tx *sql.Tx, repo string) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT number FROM issues WHERE repo = $1`, repo)
	if err != nil {
		return nil, fmt.Errorf("existing issue numbers for %s: %w", repo, err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan issue number: %w", err)
		}
		out[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("existing issue numbers for %s: %w", repo, err)
	}
	return out, nil
}

// PromoteIssue turns an inbox issue into a task: it creates the task via
// CreateTask, then marks the issue triage_state=promoted, linking task_id and
// recording appliesToVersions (marshalled to JSON). The issue must currently
// be triage_state='new' — anything else (already promoted, dismissed, or no
// such issue) is an error. eventID is passed through to CreateTask.
func PromoteIssue(tx *sql.Tx, now time.Time, repo string, number int64, in TaskInput, appliesToVersions []string, eventID int64) (*model.Task, error) {
	if err := requireNewIssue(tx, repo, number); err != nil {
		return nil, err
	}

	task, err := CreateTask(tx, now, in, eventID)
	if err != nil {
		return nil, err
	}

	versionsJSON, err := json.Marshal(appliesToVersions)
	if err != nil {
		return nil, fmt.Errorf("marshal applies_to_versions: %w", err)
	}
	res, err := tx.Exec(
		`UPDATE issues SET triage_state = 'promoted', task_id = $1, applies_to_versions = $2
		 WHERE repo = $3 AND number = $4 AND triage_state = 'new'`,
		task.ID, string(versionsJSON), repo, number,
	)
	if err != nil {
		return nil, fmt.Errorf("promote issue %s#%d: %w", repo, number, err)
	}
	if err := requireTriaged(res, repo, number); err != nil {
		return nil, err
	}
	return task, nil
}

// LinkIssue attaches an inbox issue to a task that already exists — the third
// triage outcome, for an issue whose work is already tracked. Like
// PromoteIssue it requires triage_state='new' and sets triage_state='promoted':
// "this issue has a task" is exactly what promoted means, so no new
// triage_state value (and no migration) is needed. The task must exist.
func LinkIssue(tx *sql.Tx, repo string, number int64, taskID string) error {
	if err := requireNewIssue(tx, repo, number); err != nil {
		return err
	}

	exists, err := taskExists(tx, taskID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}

	res, err := tx.Exec(
		`UPDATE issues SET triage_state = 'promoted', task_id = $1
		 WHERE repo = $2 AND number = $3 AND triage_state = 'new'`,
		taskID, repo, number,
	)
	if err != nil {
		return fmt.Errorf("link issue %s#%d to %s: %w", repo, number, taskID, err)
	}
	return requireTriaged(res, repo, number)
}

// DismissIssue marks an inbox issue triage_state=dismissed. The issue must
// currently be triage_state='new'. Returns ErrNotFound if no such issue
// exists.
func DismissIssue(tx *sql.Tx, repo string, number int64) error {
	if err := requireNewIssue(tx, repo, number); err != nil {
		return err
	}
	res, err := tx.Exec(
		`UPDATE issues SET triage_state = 'dismissed'
		 WHERE repo = $1 AND number = $2 AND triage_state = 'new'`,
		repo, number,
	)
	if err != nil {
		return fmt.Errorf("dismiss issue %s#%d: %w", repo, number, err)
	}
	return requireTriaged(res, repo, number)
}

// requireNewIssue refuses an issue that is not awaiting triage. It is the
// read half of the rule requireTriaged enforces on the write: all three
// triage outcomes open with it, so "already promoted", "already dismissed"
// and "no such issue" report the same way whichever one the caller chose.
func requireNewIssue(tx *sql.Tx, repo string, number int64) error {
	var triageState string
	err := tx.QueryRow(
		`SELECT triage_state FROM issues WHERE repo = $1 AND number = $2`, repo, number,
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
	return nil
}

// requireTriaged turns a triage UPDATE that matched no row into
// ErrBadTransition. Every triage verb reads triage_state and then writes,
// under READ COMMITTED: two concurrent calls can both read 'new', so the
// UPDATE carries the same predicate and this check is what makes the loser
// fail instead of silently overwriting the winner. The caller's transaction
// rolls back, which is also what undoes a promote's already-created task.
func requireTriaged(res sql.Result, repo string, number int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("triage issue %s#%d: %w", repo, number, err)
	}
	if n == 0 {
		return fmt.Errorf("issue %s#%d was triaged concurrently: %w", repo, number, ErrBadTransition)
	}
	return nil
}

// IssueTitle returns the title of an inbox issue inside the given
// transaction. Returns ErrNotFound if no such issue exists. Used by the
// promote handler to default a task's title to the issue's when the caller
// does not supply one.
func IssueTitle(tx *sql.Tx, repo string, number int64) (string, error) {
	var title sql.NullString
	err := tx.QueryRow(
		`SELECT title FROM issues WHERE repo = $1 AND number = $2`, repo, number,
	).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("issue %s#%d: %w", repo, number, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("get issue %s#%d title: %w", repo, number, err)
	}
	return title.String, nil
}

func scanIssue(row rowScanner) (*model.Issue, error) {
	var is model.Issue
	var title, state, taskID, appliesToVersions, url sql.NullString
	if err := row.Scan(&is.Repo, &is.Number, &title, &state, &is.TriageState,
		&taskID, &appliesToVersions, &url); err != nil {
		return nil, err
	}
	is.Title = title.String
	is.State = state.String
	is.URL = url.String
	is.TaskID = taskID.String
	if appliesToVersions.Valid {
		if err := json.Unmarshal([]byte(appliesToVersions.String), &is.AppliesToVersions); err != nil {
			return nil, fmt.Errorf("unmarshal applies_to_versions for issue %s#%d: %w", is.Repo, is.Number, err)
		}
	}
	return &is, nil
}

// ListIssues returns inbox issues, ordered by repo then number. An empty
// triageState or projectID disables that filter; a projectID with no mapped
// repos yields no issues. Issues carry a repo, and project_repos maps a repo
// to at most one project, so the project filter is a join.
func (s *Store) ListIssues(ctx context.Context, triageState, projectID string) ([]model.Issue, error) {
	q := `SELECT i.repo, i.number, i.title, i.state, i.triage_state, i.task_id,
	             i.applies_to_versions, i.url
	      FROM issues i`
	var args []any
	var where []string
	if projectID != "" {
		args = append(args, projectID)
		q += fmt.Sprintf(` JOIN project_repos pr ON pr.repo = i.repo AND pr.project_id = $%d`, len(args))
	}
	if triageState != "" {
		args = append(args, triageState)
		where = append(where, fmt.Sprintf(`i.triage_state = $%d`, len(args)))
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY i.repo, i.number`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	return collectRows(rows, "list issues", byValue(scanIssue))
}
