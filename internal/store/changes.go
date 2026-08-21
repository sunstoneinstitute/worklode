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
	// Author is the PR's GitHub login, "" when unknown (a row ingested
	// before the column existed). The self-approval check reads it.
	Author string
	// UpdatedAt is GitHub's pull_request.updated_at, the non-regression
	// guard's clock. See UpsertPR.
	UpdatedAt time.Time
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
	// UpdatedAt is GitHub's workflow_run.updated_at, the non-regression
	// guard's clock. See UpsertPR.
	UpdatedAt time.Time
}

// Review is one PR review submission.
type Review struct {
	Repo        string
	PRNumber    int64
	Reviewer    string
	State       string
	SubmittedAt time.Time
}

// bodyTaskIDPattern matches a "Worklode-Task: <ID>" trailer line (after
// trimming surrounding whitespace), capturing the task id.
//
// "Worklode-Task" is the fixed label; the id after it carries its own project
// key (WL-72, SW-3, ...). The label is deliberately not key-shaped: the
// earlier "WL-Task" read as though "WL" were a project key, in the one signal
// that most needs to be unambiguous.
var bodyTaskIDPattern = regexp.MustCompile(`^Worklode-Task:\s*([A-Z][A-Z0-9]*-[0-9]+)`)

// TaskIDFromBody scans body line by line for a "Worklode-Task: <task-id>"
// trailer (case-sensitive prefix, after trimming the line's surrounding
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
// different (or no) correlation signal. task_id sits inside the guarded SET
// below, so a delivery the guard rejects does not correlate either; a PR's
// head-ref signal is identical on every one of its events, which leaves only
// a body edit arriving out of order. author is written on insert and
// backfilled on update but never cleared: a payload without user.login (the
// import backfill) must not erase a login already known.
//
// # The non-regressing guard
//
// This is the reference site for the
//
//	WHERE coalesce(excluded.<col>, '-infinity') >= coalesce(<table>.<col>, '-infinity')
//
// clause on DO UPDATE that UpsertCIRun, UpsertIssue and CreateArtifact also
// carry. Deliveries arrive out of order — GitHub does not guarantee order,
// and reconcile replays backlogged .ignored events after newer ones already
// applied (spec 013 §2.1) — so an unconditional upsert lets a stale payload
// overwrite newer facts (a replayed pull_request.opened regressing a merged
// PR to open, dropping merge_sha and merged_at). The guard makes the write
// non-regressing; a first insert has no conflicting row and always lands.
//
// The column is the fact's own last-modified time (GitHub's updated_at /
// published_at), never the delivery time: when we received a payload says
// nothing about whether it describes newer state.
//
// It is >= and not >, so an ordinary redelivery of the same payload still
// writes. Unknown on either side sorts as '-infinity': a legacy row with no
// stored timestamp yields to the first event that carries one, so nothing
// freezes, and an event with no timestamp yields to any row that has one,
// because it cannot prove it is newer. Both unknown writes unconditionally.
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
	var updatedAt sql.NullTime
	if !pr.UpdatedAt.IsZero() {
		updatedAt = sql.NullTime{Time: pr.UpdatedAt.UTC(), Valid: true}
	}

	_, err := tx.Exec(
		`INSERT INTO pull_requests (repo, number, title, state, task_id, head_ref, head_sha, merge_sha, url, opened_at, merged_at, updated_at, author)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, nullif($13, ''))
		 ON CONFLICT (repo, number) DO UPDATE SET
		   title = excluded.title,
		   state = excluded.state,
		   head_sha = excluded.head_sha,
		   merge_sha = excluded.merge_sha,
		   url = excluded.url,
		   merged_at = excluded.merged_at,
		   updated_at = excluded.updated_at,
		   author = coalesce(excluded.author, pull_requests.author),
		   task_id = CASE WHEN pull_requests.task_id IS NULL THEN excluded.task_id ELSE pull_requests.task_id END
		 WHERE coalesce(excluded.updated_at, '-infinity') >= coalesce(pull_requests.updated_at, '-infinity')`,
		pr.Repo, pr.Number, pr.Title, pr.State, taskIDArg, pr.HeadRef, pr.HeadSHA, mergeSHA, pr.URL, openedAt, mergedAt, updatedAt, pr.Author,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert PR %s#%d: %w", pr.Repo, pr.Number, err)
	}

	return GetPRTx(tx, pr.Repo, pr.Number)
}

// ExistingPRNumbers returns the pull-request numbers already stored for repo.
// See ExistingIssueNumbers for why import needs it.
func ExistingPRNumbers(tx *sql.Tx, repo string) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT number FROM pull_requests WHERE repo = $1`, repo)
	if err != nil {
		return nil, fmt.Errorf("existing pr numbers for %s: %w", repo, err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan pr number: %w", err)
		}
		out[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("existing pr numbers for %s: %w", repo, err)
	}
	return out, nil
}

// prColumns is the SELECT list scanPR expects, in order.
const prColumns = `repo, number, title, state, task_id, head_ref, head_sha, merge_sha, url, opened_at, merged_at, updated_at, author`

func scanPR(row rowScanner) (*PullRequest, error) {
	var pr PullRequest
	var title, state, taskID, headRef, headSHA, mergeSHA, url, author sql.NullString
	var openedAt, mergedAt, updatedAt sql.NullTime
	if err := row.Scan(&pr.Repo, &pr.Number, &title, &state, &taskID,
		&headRef, &headSHA, &mergeSHA, &url, &openedAt, &mergedAt, &updatedAt,
		&author); err != nil {
		return nil, err
	}
	pr.Author = author.String
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
	if updatedAt.Valid {
		pr.UpdatedAt = updatedAt.Time.UTC()
	}
	return &pr, nil
}

// GetPRTx is GetPR inside an open transaction: the GitHub review ingest
// reads the PR it is about to act on within the delivery's own transaction.
func GetPRTx(tx *sql.Tx, repo string, number int64) (*PullRequest, error) {
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

// PRRef is the minimal PR identity the pr-affects deriver needs: which repo,
// which PR, which task.
type PRRef struct {
	Repo   string
	Number int64
	TaskID string
}

// TaskPRs returns every merged or open pull request bound to a task, ordered
// by repo then number. Two filters, both the pr-affects deriver's (007 §2.3):
//
//   - Unbound PRs are invisible: with no task there is no wl:affects subject
//     to hang the triple off.
//   - state = 'closed' — abandoned without merging — is invisible too. Its
//     changed files never landed, so an edge from it would assert the task
//     affected a component it demonstrably did not, and every such PR would
//     cost a GitHub round trip on every run, forever.
//
// PRsForTask reads one task's PRs for display and applies neither filter.
func (s *Store) TaskPRs(ctx context.Context) ([]PRRef, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, number, task_id FROM pull_requests
		 WHERE task_id IS NOT NULL AND state IN ('open', 'merged')
		 ORDER BY repo, number`)
	if err != nil {
		return nil, fmt.Errorf("task prs: %w", err)
	}
	return collectRows(rows, "task prs", func(r rowScanner) (PRRef, error) {
		var p PRRef
		err := r.Scan(&p.Repo, &p.Number, &p.TaskID)
		return p, err
	})
}

// PRsForTask returns the pull requests correlated to taskID, ordered by
// repo then number.
func (s *Store) PRsForTask(ctx context.Context, taskID string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+prColumns+` FROM pull_requests WHERE task_id = $1 ORDER BY repo, number`, taskID)
	if err != nil {
		return nil, fmt.Errorf("PRs for task %s: %w", taskID, err)
	}
	return collectRows(rows, fmt.Sprintf("PRs for task %s", taskID), byValue(scanPR))
}

// ciRunColumns is the SELECT list scanCIRun expects, in order.
const ciRunColumns = `repo, head_sha, workflow, status, conclusion, url, started_at, completed_at, updated_at`

func scanCIRun(r rowScanner) (CIRun, error) {
	var run CIRun
	var status, conclusion, url sql.NullString
	var startedAt, completedAt, updatedAt sql.NullTime
	if err := r.Scan(&run.Repo, &run.HeadSHA, &run.Workflow, &status, &conclusion,
		&url, &startedAt, &completedAt, &updatedAt); err != nil {
		return CIRun{}, err
	}
	run.Status = status.String
	run.URL = url.String
	if conclusion.Valid {
		run.Conclusion = &conclusion.String
	}
	if startedAt.Valid {
		run.StartedAt = startedAt.Time.UTC()
	}
	if completedAt.Valid {
		t := completedAt.Time.UTC()
		run.CompletedAt = &t
	}
	if updatedAt.Valid {
		run.UpdatedAt = updatedAt.Time.UTC()
	}
	return run, nil
}

// CIRunsForSHA returns the CI runs recorded for (repo, headSHA), oldest
// first.
func (s *Store) CIRunsForSHA(ctx context.Context, repo, headSHA string) ([]CIRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ciRunColumns+` FROM ci_runs WHERE repo = $1 AND head_sha = $2 ORDER BY started_at, id`,
		repo, headSHA)
	if err != nil {
		return nil, fmt.Errorf("ci runs for %s %s: %w", repo, headSHA, err)
	}
	return collectRows(rows, fmt.Sprintf("ci runs for %s %s", repo, headSHA), scanCIRun)
}

// RepoSHA names a commit in a repo: the key CI runs are recorded under.
type RepoSHA struct {
	Repo string
	SHA  string
}

// CIRunsForSHAs is the bulk form of CIRunsForSHA: the runs for every key in
// one query, keyed by (repo, head_sha). A caller reporting CI for a list of
// PRs would otherwise issue one query per PR. Keys with no runs are absent
// from the map; keys is empty-safe. Order within each slice matches
// CIRunsForSHA.
func (s *Store) CIRunsForSHAs(ctx context.Context, keys []RepoSHA) (map[RepoSHA][]CIRun, error) {
	if len(keys) == 0 {
		return map[RepoSHA][]CIRun{}, nil
	}
	repos, shas := splitRepoSHAs(keys)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ciRunColumns+` FROM ci_runs
		 WHERE (repo, head_sha) IN (SELECT * FROM unnest($1::text[], $2::text[]))
		 ORDER BY repo, head_sha, started_at, id`,
		repos, shas)
	if err != nil {
		return nil, fmt.Errorf("ci runs for %d shas: %w", len(keys), err)
	}
	return groupRows(rows, fmt.Sprintf("ci runs for %d shas", len(keys)),
		func(r rowScanner) (RepoSHA, CIRun, error) {
			run, err := scanCIRun(r)
			return RepoSHA{Repo: run.Repo, SHA: run.HeadSHA}, run, err
		})
}

func splitRepoSHAs(keys []RepoSHA) (repos, shas []string) {
	repos = make([]string, 0, len(keys))
	shas = make([]string, 0, len(keys))
	for _, k := range keys {
		repos = append(repos, k.Repo)
		shas = append(shas, k.SHA)
	}
	return repos, shas
}

func scanReview(r rowScanner) (Review, error) {
	var rv Review
	if err := r.Scan(&rv.Repo, &rv.PRNumber, &rv.Reviewer, &rv.State, &rv.SubmittedAt); err != nil {
		return Review{}, err
	}
	rv.SubmittedAt = rv.SubmittedAt.UTC()
	return rv, nil
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
	return collectRows(rows, fmt.Sprintf("reviews for %s#%d", repo, prNumber), scanReview)
}

// RepoPR names a pull request: the key reviews are recorded under.
type RepoPR struct {
	Repo   string
	Number int64
}

// ReviewsForPRs is the bulk form of ReviewsForPR: the reviews on every key in
// one query, keyed by (repo, number). Keys with no reviews are absent from the
// map; keys is empty-safe. Order within each slice matches ReviewsForPR.
func (s *Store) ReviewsForPRs(ctx context.Context, keys []RepoPR) (map[RepoPR][]Review, error) {
	if len(keys) == 0 {
		return map[RepoPR][]Review{}, nil
	}
	repos := make([]string, 0, len(keys))
	numbers := make([]int64, 0, len(keys))
	for _, k := range keys {
		repos = append(repos, k.Repo)
		numbers = append(numbers, k.Number)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT repo, pr_number, reviewer, state, submitted_at FROM reviews
		 WHERE (repo, pr_number) IN (SELECT * FROM unnest($1::text[], $2::bigint[]))
		 ORDER BY repo, pr_number, submitted_at, id`,
		repos, numbers)
	if err != nil {
		return nil, fmt.Errorf("reviews for %d prs: %w", len(keys), err)
	}
	return groupRows(rows, fmt.Sprintf("reviews for %d prs", len(keys)),
		func(r rowScanner) (RepoPR, Review, error) {
			rv, err := scanReview(r)
			return RepoPR{Repo: rv.Repo, Number: rv.PRNumber}, rv, err
		})
}

// UpsertCIRun inserts or, on redelivery, updates a CI run row. The natural
// key is (repo, head_sha, workflow, started_at). The update is guarded on
// updated_at so a stale delivery cannot regress a finished run to running —
// see UpsertPR for the guard's rationale.
func UpsertCIRun(tx *sql.Tx, r CIRun) error {
	var conclusion sql.NullString
	if r.Conclusion != nil {
		conclusion = sql.NullString{String: *r.Conclusion, Valid: true}
	}
	var completedAt sql.NullTime
	if r.CompletedAt != nil {
		completedAt = sql.NullTime{Time: r.CompletedAt.UTC(), Valid: true}
	}
	var updatedAt sql.NullTime
	if !r.UpdatedAt.IsZero() {
		updatedAt = sql.NullTime{Time: r.UpdatedAt.UTC(), Valid: true}
	}
	_, err := tx.Exec(
		`INSERT INTO ci_runs (repo, head_sha, workflow, status, conclusion, url, started_at, completed_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (repo, head_sha, workflow, started_at) DO UPDATE SET
		   status = excluded.status,
		   conclusion = excluded.conclusion,
		   url = excluded.url,
		   completed_at = excluded.completed_at,
		   updated_at = excluded.updated_at
		 WHERE coalesce(excluded.updated_at, '-infinity') >= coalesce(ci_runs.updated_at, '-infinity')`,
		r.Repo, r.HeadSHA, r.Workflow, r.Status, conclusion, r.URL,
		r.StartedAt.UTC(), completedAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert ci run %s %s %s: %w", r.Repo, r.HeadSHA, r.Workflow, err)
	}
	return nil
}

// UpsertReview inserts or, on redelivery, updates a review row. The
// natural key is (repo, pr_number, reviewer, submitted_at).
//
// It needs no non-regression guard (UpsertPR): submitted_at is already part
// of the key, so a conflict means literally the same review submission, and
// its state cannot differ between two deliveries of it.
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
