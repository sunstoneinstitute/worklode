package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// PullRequest is one GitHub pull request, correlated to at most one task.
type PullRequest struct {
	Repo     string
	Number   int64
	Title    string
	State    string
	TaskID   *string
	HeadRef  string
	HeadSHA  string
	MergeSHA *string
	URL      string
	OpenedAt time.Time
	MergedAt *time.Time
}

// CIRun is one workflow run reported for a commit.
type CIRun struct {
	Repo        string
	HeadSHA     string
	Workflow    string
	Status      string
	Conclusion  *string
	URL         string
	StartedAt   time.Time
	CompletedAt *time.Time
}

// Review is one PR review submission.
type Review struct {
	Repo        string
	PRNumber    int64
	Reviewer    string
	State       string
	SubmittedAt time.Time
}

// refTaskIDPattern matches worktree branch names of the form
// "wt/WT-<n>" or "wt/WT-<n>-<slug>", capturing the task id.
var refTaskIDPattern = regexp.MustCompile(`^wt/(WT-[0-9]+)(?:-.*)?$`)

// TaskIDFromRef extracts a task id from a branch name following the
// "wt/<task-id>-<slug>" convention (the slug is optional). It returns "" if
// ref does not match — including when the id part uses a lowercase "wt-"
// prefix, since task ids are always uppercase "WT-".
func TaskIDFromRef(ref string) string {
	m := refTaskIDPattern.FindStringSubmatch(ref)
	if m == nil {
		return ""
	}
	return m[1]
}

// bodyTaskIDPattern matches a "WT-Task: WT-<n>" marker line (after
// trimming surrounding whitespace), capturing the task id.
var bodyTaskIDPattern = regexp.MustCompile(`^WT-Task:\s*(WT-[0-9]+)`)

// TaskIDFromBody scans body line by line for a "WT-Task: <task-id>" marker
// (case-sensitive prefix, after trimming the line's surrounding
// whitespace) and returns the task id from the first line found. Returns ""
// if no such line exists.
func TaskIDFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if m := bodyTaskIDPattern.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// taskExists reports whether a task with the given id exists, inside tx.
func taskExists(tx *sql.Tx, taskID string) (bool, error) {
	if taskID == "" {
		return false, nil
	}
	var one int
	err := tx.QueryRow(`SELECT 1 FROM tasks WHERE id = ?`, taskID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check task %s exists: %w", taskID, err)
	}
	return true, nil
}

// UpsertPR inserts a new PR row, or on redelivery updates
// title/state/head_sha/merge_sha/url/merged_at. Task correlation: on
// insert, the task id is taken from head_ref (TaskIDFromRef) or, failing
// that, from body (TaskIDFromBody); it is only set if that task actually
// exists. On update, task_id is set only if it is currently NULL — once
// correlated, a PR keeps its task link even if a later delivery carries a
// different (or no) correlation signal.
func UpsertPR(tx *sql.Tx, pr PullRequest, body string) (*PullRequest, error) {
	candidate := TaskIDFromRef(pr.HeadRef)
	if candidate == "" {
		candidate = TaskIDFromBody(body)
	}
	var taskID *string
	if candidate != "" {
		exists, err := taskExists(tx, candidate)
		if err != nil {
			return nil, err
		}
		if exists {
			taskID = &candidate
		}
	}

	var mergedAt sql.NullString
	if pr.MergedAt != nil {
		mergedAt = sql.NullString{String: pr.MergedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	var mergeSHA sql.NullString
	if pr.MergeSHA != nil {
		mergeSHA = sql.NullString{String: *pr.MergeSHA, Valid: true}
	}
	var openedAt sql.NullString
	if !pr.OpenedAt.IsZero() {
		openedAt = sql.NullString{String: pr.OpenedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	var taskIDArg sql.NullString
	if taskID != nil {
		taskIDArg = sql.NullString{String: *taskID, Valid: true}
	}

	_, err := tx.Exec(
		`INSERT INTO pull_requests (repo, number, title, state, task_id, head_ref, head_sha, merge_sha, url, opened_at, merged_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (repo, number) DO UPDATE SET
		   title = excluded.title,
		   state = excluded.state,
		   head_sha = excluded.head_sha,
		   merge_sha = excluded.merge_sha,
		   url = excluded.url,
		   merged_at = excluded.merged_at,
		   task_id = CASE WHEN pull_requests.task_id IS NULL THEN excluded.task_id ELSE pull_requests.task_id END`,
		pr.Repo, pr.Number, pr.Title, pr.State, taskIDArg, pr.HeadRef, pr.HeadSHA, mergeSHA, pr.URL, openedAt, mergedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert PR %s#%d: %w", pr.Repo, pr.Number, err)
	}

	return getPRTx(tx, pr.Repo, pr.Number)
}

// prColumns is the SELECT list scanPR expects, in order.
const prColumns = `repo, number, title, state, task_id, head_ref, head_sha, merge_sha, url, opened_at, merged_at`

func scanPR(row rowScanner) (*PullRequest, error) {
	var pr PullRequest
	var title, state, taskID, headRef, headSHA, mergeSHA, url, openedAt, mergedAt sql.NullString
	if err := row.Scan(&pr.Repo, &pr.Number, &title, &state, &taskID,
		&headRef, &headSHA, &mergeSHA, &url, &openedAt, &mergedAt); err != nil {
		return nil, err
	}
	pr.Title = title.String
	pr.State = state.String
	pr.HeadRef = headRef.String
	pr.HeadSHA = headSHA.String
	pr.URL = url.String
	if taskID.Valid {
		pr.TaskID = &taskID.String
	}
	if mergeSHA.Valid {
		pr.MergeSHA = &mergeSHA.String
	}
	if openedAt.Valid {
		t, err := time.Parse(time.RFC3339, openedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse PR %s#%d opened_at: %w", pr.Repo, pr.Number, err)
		}
		pr.OpenedAt = t
	}
	if mergedAt.Valid {
		t, err := time.Parse(time.RFC3339, mergedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse PR %s#%d merged_at: %w", pr.Repo, pr.Number, err)
		}
		pr.MergedAt = &t
	}
	return &pr, nil
}

func getPRTx(tx *sql.Tx, repo string, number int64) (*PullRequest, error) {
	row := tx.QueryRow(`SELECT `+prColumns+` FROM pull_requests WHERE repo = ? AND number = ?`, repo, number)
	pr, err := scanPR(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("PR %s#%d: %w", repo, number, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get PR %s#%d: %w", repo, number, err)
	}
	return pr, nil
}

// GetPR looks up a pull request by repo and number. Returns ErrNotFound if
// it does not exist.
func (s *Store) GetPR(ctx context.Context, repo string, number int64) (*PullRequest, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+prColumns+` FROM pull_requests WHERE repo = ? AND number = ?`, repo, number)
	pr, err := scanPR(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("PR %s#%d: %w", repo, number, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get PR %s#%d: %w", repo, number, err)
	}
	return pr, nil
}

// PRsForTask returns the pull requests correlated to taskID, ordered by
// repo then number.
func (s *Store) PRsForTask(ctx context.Context, taskID string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+prColumns+` FROM pull_requests WHERE task_id = ? ORDER BY repo, number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("PRs for task %s: %w", taskID, err)
	}
	defer rows.Close()

	var out []PullRequest
	for rows.Next() {
		pr, err := scanPR(rows)
		if err != nil {
			return nil, fmt.Errorf("scan PR: %w", err)
		}
		out = append(out, *pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PRs for task %s: %w", taskID, err)
	}
	return out, nil
}

// UpsertCIRun inserts or, on redelivery, updates a CI run row. The natural
// key is (repo, head_sha, workflow, started_at).
func UpsertCIRun(tx *sql.Tx, r CIRun) error {
	var conclusion sql.NullString
	if r.Conclusion != nil {
		conclusion = sql.NullString{String: *r.Conclusion, Valid: true}
	}
	var completedAt sql.NullString
	if r.CompletedAt != nil {
		completedAt = sql.NullString{String: r.CompletedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO ci_runs (repo, head_sha, workflow, status, conclusion, url, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (repo, head_sha, workflow, started_at) DO UPDATE SET
		   status = excluded.status,
		   conclusion = excluded.conclusion,
		   url = excluded.url,
		   completed_at = excluded.completed_at`,
		r.Repo, r.HeadSHA, r.Workflow, r.Status, conclusion, r.URL,
		r.StartedAt.UTC().Format(time.RFC3339), completedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert ci run %s %s %s: %w", r.Repo, r.HeadSHA, r.Workflow, err)
	}
	return nil
}

// UpsertReview inserts or, on redelivery, updates a review row. The
// natural key is (repo, pr_number, reviewer, submitted_at).
func UpsertReview(tx *sql.Tx, rv Review) error {
	_, err := tx.Exec(
		`INSERT INTO reviews (repo, pr_number, reviewer, state, submitted_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (repo, pr_number, reviewer, submitted_at) DO UPDATE SET
		   state = excluded.state`,
		rv.Repo, rv.PRNumber, rv.Reviewer, rv.State, rv.SubmittedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert review %s#%d by %s: %w", rv.Repo, rv.PRNumber, rv.Reviewer, err)
	}
	return nil
}
