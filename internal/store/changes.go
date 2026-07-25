package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
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

// branchPrefixPattern matches task branches "<prefix><ID>[-slug]" for the
// configured prefix plus the legacy "wl/". Rebuilt by SetBranchPrefix;
// guarded because webhook handlers read it concurrently.
var (
	branchPatternMu     sync.RWMutex
	branchPrefix        = defaultBranchPrefix
	branchPrefixPattern = buildBranchPattern(defaultBranchPrefix)
)

const defaultBranchPrefix = "lode/"

func buildBranchPattern(prefix string) *regexp.Regexp {
	alts := regexp.QuoteMeta(prefix)
	if prefix != "wl/" {
		alts += "|wl/"
	}
	return regexp.MustCompile(`^(?:` + alts + `)([A-Z][A-Z0-9]*-[0-9]+)(?:-.*)?$`)
}

// SetBranchPrefix configures the task-branch prefix (LODE_BRANCH_PREFIX,
// default "lode/"). The legacy "wl/" prefix is always also recognized.
func SetBranchPrefix(prefix string) {
	if prefix == "" {
		prefix = defaultBranchPrefix
	}
	branchPatternMu.Lock()
	defer branchPatternMu.Unlock()
	branchPrefix = prefix
	branchPrefixPattern = buildBranchPattern(prefix)
}

// BranchPrefix returns the configured task-branch prefix.
func BranchPrefix() string {
	branchPatternMu.RLock()
	defer branchPatternMu.RUnlock()
	return branchPrefix
}

// TaskIDFromRef extracts a task id from a branch name following the
// "<prefix><task-id>-<slug>" convention (the slug is optional). It returns ""
// if ref does not match — including when the id part uses a lowercase prefix,
// since task-id prefixes are always uppercase (e.g. WL-, SW-).
func TaskIDFromRef(ref string) string {
	branchPatternMu.RLock()
	defer branchPatternMu.RUnlock()
	m := branchPrefixPattern.FindStringSubmatch(ref)
	if m == nil {
		return ""
	}
	return m[1]
}

// bodyTaskIDPattern matches a "WL-Task: <ID>" marker line (after trimming
// surrounding whitespace), capturing the task id. "WL-Task" is the fixed
// marker label, not the id prefix.
var bodyTaskIDPattern = regexp.MustCompile(`^WL-Task:\s*([A-Z][A-Z0-9]*-[0-9]+)`)

// TaskIDFromBody scans body line by line for a "WL-Task: <task-id>" marker
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
	err := tx.QueryRow(`SELECT 1 FROM tasks WHERE id = $1`, taskID).Scan(&one)
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

	var mergedAt sql.NullTime
	if pr.MergedAt != nil {
		mergedAt = sql.NullTime{Time: pr.MergedAt.UTC(), Valid: true}
	}
	var mergeSHA sql.NullString
	if pr.MergeSHA != nil {
		mergeSHA = sql.NullString{String: *pr.MergeSHA, Valid: true}
	}
	var openedAt sql.NullTime
	if !pr.OpenedAt.IsZero() {
		openedAt = sql.NullTime{Time: pr.OpenedAt.UTC(), Valid: true}
	}
	var taskIDArg sql.NullString
	if taskID != nil {
		taskIDArg = sql.NullString{String: *taskID, Valid: true}
	}

	_, err := tx.Exec(
		`INSERT INTO pull_requests (repo, number, title, state, task_id, head_ref, head_sha, merge_sha, url, opened_at, merged_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
	var title, state, taskID, headRef, headSHA, mergeSHA, url sql.NullString
	var openedAt, mergedAt sql.NullTime
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
		pr.OpenedAt = openedAt.Time.UTC()
	}
	if mergedAt.Valid {
		t := mergedAt.Time.UTC()
		pr.MergedAt = &t
	}
	return &pr, nil
}

func getPRTx(tx *sql.Tx, repo string, number int64) (*PullRequest, error) {
	row := tx.QueryRow(`SELECT `+prColumns+` FROM pull_requests WHERE repo = $1 AND number = $2`, repo, number)
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
	row := s.db.QueryRowContext(ctx, `SELECT `+prColumns+` FROM pull_requests WHERE repo = $1 AND number = $2`, repo, number)
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
		`SELECT `+prColumns+` FROM pull_requests WHERE task_id = $1 ORDER BY repo, number`, taskID)
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

// CIRunsForSHA returns the CI runs recorded for (repo, headSHA), oldest
// first.
func (s *Store) CIRunsForSHA(ctx context.Context, repo, headSHA string) ([]CIRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, head_sha, workflow, status, conclusion, url, started_at, completed_at
		 FROM ci_runs WHERE repo = $1 AND head_sha = $2 ORDER BY started_at, id`,
		repo, headSHA)
	if err != nil {
		return nil, fmt.Errorf("ci runs for %s %s: %w", repo, headSHA, err)
	}
	defer rows.Close()

	var out []CIRun
	for rows.Next() {
		var r CIRun
		var status, conclusion, url sql.NullString
		var startedAt, completedAt sql.NullTime
		if err := rows.Scan(&r.Repo, &r.HeadSHA, &r.Workflow, &status, &conclusion,
			&url, &startedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan ci run: %w", err)
		}
		r.Status = status.String
		r.URL = url.String
		if conclusion.Valid {
			r.Conclusion = &conclusion.String
		}
		if startedAt.Valid {
			r.StartedAt = startedAt.Time.UTC()
		}
		if completedAt.Valid {
			t := completedAt.Time.UTC()
			r.CompletedAt = &t
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ci runs for %s %s: %w", repo, headSHA, err)
	}
	return out, nil
}

// ReviewsForPR returns the reviews submitted on (repo, prNumber), oldest
// first.
func (s *Store) ReviewsForPR(ctx context.Context, repo string, prNumber int64) ([]Review, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, pr_number, reviewer, state, submitted_at
		 FROM reviews WHERE repo = $1 AND pr_number = $2 ORDER BY submitted_at, id`,
		repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("reviews for %s#%d: %w", repo, prNumber, err)
	}
	defer rows.Close()

	var out []Review
	for rows.Next() {
		var rv Review
		if err := rows.Scan(&rv.Repo, &rv.PRNumber, &rv.Reviewer, &rv.State, &rv.SubmittedAt); err != nil {
			return nil, fmt.Errorf("scan review: %w", err)
		}
		rv.SubmittedAt = rv.SubmittedAt.UTC()
		out = append(out, rv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reviews for %s#%d: %w", repo, prNumber, err)
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
	var completedAt sql.NullTime
	if r.CompletedAt != nil {
		completedAt = sql.NullTime{Time: r.CompletedAt.UTC(), Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO ci_runs (repo, head_sha, workflow, status, conclusion, url, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (repo, head_sha, workflow, started_at) DO UPDATE SET
		   status = excluded.status,
		   conclusion = excluded.conclusion,
		   url = excluded.url,
		   completed_at = excluded.completed_at`,
		r.Repo, r.HeadSHA, r.Workflow, r.Status, conclusion, r.URL,
		r.StartedAt.UTC(), completedAt,
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
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (repo, pr_number, reviewer, submitted_at) DO UPDATE SET
		   state = excluded.state`,
		rv.Repo, rv.PRNumber, rv.Reviewer, rv.State, rv.SubmittedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert review %s#%d by %s: %w", rv.Repo, rv.PRNumber, rv.Reviewer, err)
	}
	return nil
}
