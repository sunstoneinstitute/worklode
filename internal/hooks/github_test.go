package hooks_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const testSecret = "test-webhook-secret"

// dbEnv is the half of a webhook test fixture both handlers share: the
// store, and the raw-SQL assertions that read it. Raw SQL goes through the
// store's own connection pool.
type dbEnv struct {
	st *store.Store
}

// env is a webhook test fixture: a real store with repo
// sunstoneinstitute/demo mapped to project "demo" and the GitHub handler.
type env struct {
	dbEnv
	h http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWithBranchResolver(t, nil)
}

// newEnvWithBranchResolver builds an env like newEnv, wiring resolveBranch
// into the handler so a release's target_commitish resolves through it
// instead of the (disabled by default) GitHub App lookup. A nil resolveBranch
// matches newEnv: resolution disabled, same as no App configured.
func newEnvWithBranchResolver(t *testing.T, resolveBranch func(repo, branch string) (string, error)) *env {
	t.Helper()
	return newEnvWith(t, resolveBranch, nil)
}

// newEnvWithMetrics builds an env whose handler records into m, so a test can
// assert on the webhook counters.
func newEnvWithMetrics(t *testing.T, m *hooks.Metrics) *env {
	t.Helper()
	return newEnvWith(t, nil, m)
}

func newEnvWith(t *testing.T, resolveBranch func(repo, branch string) (string, error), m *hooks.Metrics) *env {
	t.Helper()
	st := store.OpenTestStore(t)

	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "sunstoneinstitute/demo"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	var resolve func(ctx context.Context, repo, branch string) (string, error)
	if resolveBranch != nil {
		resolve = func(_ context.Context, repo, branch string) (string, error) {
			return resolveBranch(repo, branch)
		}
	}
	return &env{
		dbEnv: dbEnv{st: st},
		h:     hooks.NewGitHubHandlerWithResolver(st, testSecret, slog.Default(), nil, resolve, m),
	}
}

// seedReviewer creates a human actor whose expected_github_login is login,
// which is what maps a GitHub reviewer to an actor id.
func (e *env) seedReviewer(t *testing.T, id, login string) {
	t.Helper()
	if err := e.st.UpsertHumanActor(context.Background(), id, id, false, login, "", nil); err != nil {
		t.Fatalf("seed reviewer %s: %v", id, err)
	}
}

// approvalRow reads the single approval for demoRepo#42.
func (e *env) approvalRow(t *testing.T, number int64) (state string, requiredActor, resolvingActor *string, resolvedAt *time.Time) {
	t.Helper()
	var ra, sa sql.NullString
	var at sql.NullTime
	if !e.rawQueryRow(t, []any{&state, &ra, &sa, &at},
		`SELECT state, required_actor, resolving_actor, resolved_at FROM approvals
		 WHERE entity_kind = 'pr' AND entity_id = $1`,
		store.PREntityID(demoRepo, number)) {
		t.Fatalf("no approval row for %s", store.PREntityID(demoRepo, number))
	}
	if ra.Valid {
		requiredActor = &ra.String
	}
	if sa.Valid {
		resolvingActor = &sa.String
	}
	if at.Valid {
		t := at.Time.UTC()
		resolvedAt = &t
	}
	return state, requiredActor, resolvingActor, resolvedAt
}

func (e *env) approvalCount(t *testing.T) int {
	return e.rawQueryInt(t, `SELECT COUNT(*) FROM approvals`)
}

// seedTask creates a task in project "demo" (state ready) and returns its id.
func (e *env) seedTask(t *testing.T) string {
	t.Helper()
	var id string
	_, _, err := e.st.RecordEvent(context.Background(), "cli", "seed:"+t.Name(), "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, e.st.Now(), store.TaskInput{
				ProjectID: "demo", Title: "fix crash", Priority: "medium", Kind: "bug",
			}, eventID)
			if err != nil {
				return err
			}
			id = task.ID
			return nil
		})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return id
}

// claimTask moves a seeded task to in_progress with an active lease.
func (e *env) claimTask(t *testing.T, taskID string) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.CreateActor(ctx, "agent", "agent", "Agent", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	if _, err := e.st.Claim(ctx, taskID, "agent", "host:/wt-"+taskID, 0); err != nil {
		t.Fatalf("claim task: %v", err)
	}
}

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "github", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// deliver posts a signed webhook to the handler. fixtureFile "" means body
// is used verbatim; otherwise the named testdata file is the body.
func deliver(t *testing.T, h http.Handler, event, deliveryID, fixtureFile string) *httptest.ResponseRecorder {
	t.Helper()
	return deliverBody(t, h, event, deliveryID, fixture(t, fixtureFile))
}

func deliverBody(t *testing.T, h http.Handler, event, deliveryID string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body))
	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}
	if deliveryID != "" {
		req.Header.Set("X-GitHub-Delivery", deliveryID)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// deliverOK posts a fixture and requires a clean "ok" — an apply that errors
// rolls its whole transaction back, which otherwise looks indistinguishable
// from "the handler correctly did nothing".
func deliverOK(t *testing.T, e *env, event, deliveryID, fixtureFile string) {
	t.Helper()
	rr := deliver(t, e.h, event, deliveryID, fixtureFile)
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("deliver %s %s (%s): code=%d body=%s",
			event, fixtureFile, deliveryID, rr.Code, rr.Body.String())
	}
}

// setDoneState overrides the repo mapping's terminal delivery state.
func (e *env) setDoneState(t *testing.T, repo, doneState string) {
	t.Helper()
	if _, err := e.st.DBForTests().Exec(
		`UPDATE project_repos SET done_state = $1 WHERE repo = $2`, doneState, repo); err != nil {
		t.Fatalf("set done_state %s for %s: %v", doneState, repo, err)
	}
}

// ackStatus decodes the "status" field of a webhook ack body.
func ackStatus(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return m["status"]
}

// rawQueryInt runs a single-value SQL query against the store's database.
func (e *dbEnv) rawQueryInt(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.st.DBForTests().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return n
}

// rawQueryRow scans one multi-column row, reporting whether it existed.
func (e *dbEnv) rawQueryRow(t *testing.T, dest []any, query string, args ...any) bool {
	t.Helper()
	err := e.st.DBForTests().QueryRow(query, args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return true
}

// rawQueryString runs a single-value SQL query against the store's database.
func (e *dbEnv) rawQueryString(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := e.st.DBForTests().QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return s
}

func (e *env) eventCount(t *testing.T) int {
	return e.rawQueryInt(t, `SELECT COUNT(*) FROM events WHERE source = 'github'`)
}

func (e *env) eventType(t *testing.T, deliveryID string) string {
	t.Helper()
	var typ string
	if err := e.st.DBForTests().QueryRow(
		`SELECT type FROM events WHERE source = 'github' AND external_id = $1`, deliveryID,
	).Scan(&typ); err != nil {
		t.Fatalf("event type for %s: %v", deliveryID, err)
	}
	return typ
}

func TestSignatureRejected(t *testing.T) {
	e := newEnv(t)
	body := fixture(t, "issues_opened.json")

	t.Run("missing signature", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", "issues")
		req.Header.Set("X-GitHub-Delivery", "d-nosig")
		rr := httptest.NewRecorder()
		e.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rr.Code)
		}
	})
	t.Run("bad signature", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/hooks/github", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(make([]byte, 32)))
		req.Header.Set("X-GitHub-Event", "issues")
		req.Header.Set("X-GitHub-Delivery", "d-badsig")
		rr := httptest.NewRecorder()
		e.h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rr.Code)
		}
	})
	if n := e.eventCount(t); n != 0 {
		t.Fatalf("events recorded for rejected deliveries = %d, want 0", n)
	}
}

func TestMissingHeaders(t *testing.T) {
	e := newEnv(t)
	if rr := deliver(t, e.h, "", "d-1", "issues_opened.json"); rr.Code != http.StatusBadRequest {
		t.Fatalf("missing event header: status = %d, want 400", rr.Code)
	}
	if rr := deliver(t, e.h, "issues", "", "issues_opened.json"); rr.Code != http.StatusBadRequest {
		t.Fatalf("missing delivery id: status = %d, want 400", rr.Code)
	}
	if n := e.eventCount(t); n != 0 {
		t.Fatalf("events recorded for 400s = %d, want 0", n)
	}
}

func TestEmptySecretIs503(t *testing.T) {
	e := newEnv(t)
	h := hooks.NewGitHubHandler(e.st, "", slog.Default(), nil, nil, nil)
	rr := deliver(t, h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestIdempotency(t *testing.T) {
	e := newEnv(t)
	rr := deliver(t, e.h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("first delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = deliver(t, e.h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "duplicate" {
		t.Fatalf("second delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.eventCount(t); n != 1 {
		t.Fatalf("event rows = %d, want 1", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM issues`); n != 1 {
		t.Fatalf("issue rows = %d, want 1", n)
	}
}

func TestIssuesOpenedThenClosed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	deliver(t, e.h, "issues", "d-1", "issues_opened.json")
	issues, err := e.st.ListIssues(ctx, "", "")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %v, err = %v, want 1 row", issues, err)
	}
	is := issues[0]
	if is.Repo != "sunstoneinstitute/demo" || is.Number != 7 || is.Title != "Crash on load" ||
		is.State != "open" || is.TriageState != "new" ||
		is.URL != "https://github.com/sunstoneinstitute/demo/issues/7" {
		t.Fatalf("issue = %+v", is)
	}
	if typ := e.eventType(t, "d-1"); typ != "issues.opened" {
		t.Fatalf("event type = %q, want issues.opened", typ)
	}

	deliver(t, e.h, "issues", "d-2", "issues_closed.json")
	issues, err = e.st.ListIssues(ctx, "", "")
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %v, err = %v, want 1 row", issues, err)
	}
	is = issues[0]
	if is.State != "closed" || is.Title != "Crash on load (fixed)" || is.TriageState != "new" {
		t.Fatalf("issue after close = %+v", is)
	}
}

func TestPROpenedCorrelatesAndMovesToReview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	taskID := e.seedTask(t) // WL-1
	e.claimTask(t, taskID)  // in_progress + active lease

	rr := deliver(t, e.h, "pull_request", "d-1", "pull_request_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	pr, err := e.st.GetPR(ctx, "sunstoneinstitute/demo", 42)
	if err != nil {
		t.Fatalf("get PR: %v", err)
	}
	if pr.TaskID == nil || *pr.TaskID != taskID {
		t.Fatalf("PR task id = %v, want %s", pr.TaskID, taskID)
	}
	if pr.State != "open" || pr.HeadRef != "WL-1-x" || pr.MergeSHA != nil ||
		pr.Title != "Fix crash on load" || pr.OpenedAt.IsZero() {
		t.Fatalf("PR = %+v", pr)
	}
	task, err := e.st.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.State != "in_review" {
		t.Fatalf("task state = %q, want in_review", task.State)
	}
}

func TestPROpenedTaskNotInProgressSkipsTransition(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	taskID := e.seedTask(t) // WL-1 stays in ready

	rr := deliver(t, e.h, "pull_request", "d-1", "pull_request_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	pr, err := e.st.GetPR(ctx, "sunstoneinstitute/demo", 42)
	if err != nil || pr.TaskID == nil || *pr.TaskID != taskID {
		t.Fatalf("PR = %+v, err = %v, want correlated to %s", pr, err, taskID)
	}
	task, err := e.st.GetTask(ctx, taskID)
	if err != nil || task.State != "ready" {
		t.Fatalf("task state = %v (err %v), want ready", task, err)
	}
}

// prHeadSHA and prMergeSHA are the shas in pull_request_closed_merged.json;
// push_main_pr_merge.json lands exactly those two on main. Its merge subject
// ("... from contributor/patch-1") deliberately names no task branch, so the
// push contributes no attribution of its own: whatever advances the task in
// these tests came from the PR facts.
const (
	prHeadSHA  = "abc1230000000000000000000000000000000000"
	prMergeSHA = "def4560000000000000000000000000000000000"
)

// TestPRMergeRecordsFactsAndResolves: a merged PR records its head and merge
// shas as task commits; the task reaches merged only once those shas are
// known to be on main. Both arrival orders must end in the same state.
func TestPRMergeRecordsFactsAndResolves(t *testing.T) {
	t.Run("PR merged then push to main", func(t *testing.T) {
		e := newEnv(t)
		ctx := context.Background()
		taskID := e.seedTask(t)
		e.claimTask(t, taskID)
		deliverOK(t, e, "pull_request", "d-1", "pull_request_opened.json") // → in_review

		deliverOK(t, e, "pull_request", "d-2", "pull_request_closed_merged.json")

		pr, err := e.st.GetPR(ctx, demoRepo, 42)
		if err != nil {
			t.Fatalf("get PR: %v", err)
		}
		if pr.State != "merged" || pr.MergeSHA == nil || pr.MergedAt == nil {
			t.Fatalf("PR after merge = %+v", pr)
		}
		// The merge says nothing about whether the worktree is still
		// occupied, so the lease stays open.
		if _, err := e.st.ActiveLease(ctx, taskID); err != nil {
			t.Fatalf("active lease after PR merge: err = %v, want it still open", err)
		}
		for _, sha := range []string{prHeadSHA, prMergeSHA} {
			if src := e.taskCommitSource(t, taskID, demoRepo, sha); src != "pr" {
				t.Fatalf("task_commit source for %s = %q, want pr", sha, src)
			}
		}
		// Nothing is on main yet: the merge event alone must not deliver.
		if st := e.taskState(t, taskID); st != "in_review" {
			t.Fatalf("task state after PR merge = %q, want in_review (nothing on main yet)", st)
		}

		deliverPushOK(t, e, "d-3", "push_main_pr_merge.json")
		if st := e.taskState(t, taskID); st != "merged" {
			t.Fatalf("task state after push = %q, want merged", st)
		}
	})

	t.Run("push to main then PR merged", func(t *testing.T) {
		e := newEnv(t)
		taskID := e.seedTask(t)
		e.claimTask(t, taskID)
		deliverOK(t, e, "pull_request", "d-1", "pull_request_opened.json") // → in_review

		deliverPushOK(t, e, "d-2", "push_main_pr_merge.json")
		// The push carries no attributable subject and no prior task
		// commits, so it records main commits and nothing else.
		if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 0 {
			t.Fatalf("task_commit rows after push = %d, want 0", n)
		}
		if st := e.taskState(t, taskID); st != "in_review" {
			t.Fatalf("task state after push = %q, want in_review (no attribution yet)", st)
		}

		deliverOK(t, e, "pull_request", "d-3", "pull_request_closed_merged.json")
		if st := e.taskState(t, taskID); st != "merged" {
			t.Fatalf("task state after PR merge = %q, want merged", st)
		}
	})
}

func TestReviewSubmitted(t *testing.T) {
	e := newEnv(t)
	rr := deliver(t, e.h, "pull_request_review", "d-1", "pull_request_review_submitted.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	reviews, err := e.st.ReviewsForPR(context.Background(), "sunstoneinstitute/demo", 42)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("reviews = %v, err = %v, want 1", reviews, err)
	}
	rv := reviews[0]
	if rv.Reviewer != "bob" || rv.State != "approved" || rv.SubmittedAt.IsZero() {
		t.Fatalf("review = %+v", rv)
	}
}

func TestWorkflowRunCompleted(t *testing.T) {
	e := newEnv(t)
	rr := deliver(t, e.h, "workflow_run", "d-1", "workflow_run_completed.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	runs, err := e.st.CIRunsForSHA(context.Background(),
		"sunstoneinstitute/demo", "abc1230000000000000000000000000000000000")
	if err != nil || len(runs) != 1 {
		t.Fatalf("ci runs = %v, err = %v, want 1", runs, err)
	}
	r := runs[0]
	if r.Workflow != "CI" || r.Status != "completed" ||
		r.Conclusion == nil || *r.Conclusion != "success" ||
		r.CompletedAt == nil || r.StartedAt.IsZero() {
		t.Fatalf("ci run = %+v", r)
	}
}

func TestReleasePublished(t *testing.T) {
	e := newEnv(t)
	rr := deliver(t, e.h, "release", "d-1", "release_published.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	// No main commit has ever been seen for the repo, so the frontier is
	// unresolvable — but target_commitish is already a sha, which the artifact
	// records regardless. Look the artifact up by kind/name/version, so the
	// query names the row rather than depending on the sha it carries.
	var sha, repo string
	var builtAt time.Time
	if !e.rawQueryRow(t, []any{&sha, &repo, &builtAt},
		`SELECT source_sha, repo, built_at FROM artifacts
		  WHERE kind = 'git_tag' AND name = $1 AND version = 'v1.2.3'`,
		demoRepo) {
		t.Fatal("no git_tag artifact for v1.2.3")
	}
	if sha != "abc1230000000000000000000000000000000000" {
		t.Fatalf("artifact source_sha = %q, want the target_commitish", sha)
	}
	if repo != demoRepo {
		t.Fatalf("artifact repo = %q, want %q", repo, demoRepo)
	}
	if builtAt.IsZero() {
		t.Fatal("artifact built_at is zero")
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM release_frontiers`); n != 0 {
		t.Fatalf("release_frontiers rows = %d, want 0", n)
	}
}

// TestReleaseArtifactKeepsAnUnlandedCommitish: a target_commitish that is an
// explicit sha absent from main_commits (a backport tag, a release cut before
// the repo was onboarded) is what the artifact records. Falling back to the
// frontier's sha here would attribute the tag to a commit it does not contain
// and make main's head correlate to the wrong artifact.
func TestReleaseArtifactKeepsAnUnlandedCommitish(t *testing.T) {
	e := newEnv(t)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")
	const backportSHA = "7777777777777777777777777777777777777777"
	const headSHA = "3333333333333333333333333333333333333333"

	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v0.9.1", backportSHA))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	sha := e.rawQueryString(t,
		`SELECT source_sha FROM artifacts WHERE kind = 'git_tag' AND version = 'v0.9.1'`)
	if sha != backportSHA {
		t.Fatalf("artifact source_sha = %q, want the unlanded target_commitish %q", sha, backportSHA)
	}
	// The frontier is a main-commit id, so it still falls back to main's head.
	if got, want := e.rawQueryInt(t,
		`SELECT main_id FROM release_frontiers WHERE repo = $1 AND tag = 'v0.9.1'`, demoRepo),
		e.mainCommitID(t, headSHA); got != want {
		t.Fatalf("frontier main_id = %d, want %d (main head)", got, want)
	}
}

// TestReleaseSetsFrontier: for a release-based repo, a published release
// covers every main commit seen so far and takes landed tasks to released.
func TestReleaseSetsFrontier(t *testing.T) {
	e := newEnv(t)
	e.setDoneState(t, demoRepo, "released")
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state before release = %q, want merged", st)
	}

	deliverOK(t, e, "release", "d-3", "release_published.json")

	if st := e.taskState(t, taskID); st != "released" {
		t.Fatalf("task state = %q, want released", st)
	}
	head := e.mainCommitID(t, "3333333333333333333333333333333333333333")
	got := e.rawQueryInt(t,
		`SELECT main_id FROM release_frontiers WHERE repo = $1 AND tag = 'v1.2.3'`, demoRepo)
	if got != head {
		t.Fatalf("release_frontiers main_id = %d, want %d (head of main)", got, head)
	}
}

// releaseBody builds a release.published payload for an explicit tag and
// target commitish.
func releaseBody(tag, targetCommitish string) []byte {
	return []byte(`{
		"action": "published",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"release": {
			"tag_name": "` + tag + `",
			"target_commitish": "` + targetCommitish + `",
			"published_at": "2026-07-19T12:00:00Z"
		}
	}`)
}

// TestReleaseResolvesBranchCommitish: a release cut from a release branch
// resolves that branch to its head commit through the GitHub App, so the
// artifact names the branch tip — a real, GitHub-verified commit — even
// though that commit has never landed on main. The frontier stays main-based
// (task delivery tracks through main): it falls back to main's head, the
// same as an unresolved commitish would, since the resolved sha is not a
// known main commit either.
func TestReleaseResolvesBranchCommitish(t *testing.T) {
	e := newEnvWithBranchResolver(t, func(repo, branch string) (string, error) {
		if repo == demoRepo && branch == "release-1.2" {
			return "9999999999999999999999999999999999999999", nil
		}
		return "", nil
	})
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v1.2.4", "release-1.2"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	arts, err := e.st.ArtifactsBySourceSHA(context.Background(),
		"9999999999999999999999999999999999999999")
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts = %v, err = %v, want 1", arts, err)
	}

	// The resolved branch tip never landed on main, so it cannot become the
	// frontier: over-marking delivery from an unlanded commit would be worse
	// than the pre-Task-4 main-head approximation this replaces.
	got := e.rawQueryInt(t,
		`SELECT main_id FROM release_frontiers WHERE repo = $1 AND tag = 'v1.2.4'`, demoRepo)
	if want := e.mainCommitID(t, "3333333333333333333333333333333333333333"); got != want {
		t.Fatalf("frontier main_id = %d, want %d (main head, not the resolved branch sha)", got, want)
	}
}

// TestReleaseUnresolvableBranchFallsBackToMainHead: with no App configured
// the handler keeps the release-on-merge fallback rather than failing.
func TestReleaseUnresolvableBranchFallsBackToMainHead(t *testing.T) {
	e := newEnv(t) // nil resolver
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v1.2.4", "release-1.2"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	got := e.rawQueryInt(t,
		`SELECT main_id FROM release_frontiers WHERE repo = $1 AND tag = 'v1.2.4'`, demoRepo)
	if got != e.mainCommitID(t, "3333333333333333333333333333333333333333") {
		t.Fatalf("frontier = %d, want main head", got)
	}
}

// TestReleaseArtifactUsesResolvedSHA: a release whose target_commitish is a
// branch name must store the commit the frontier resolved to, not the branch
// name — an artifact whose source_sha is "main" can never correlate to a
// Flux revision.
func TestReleaseArtifactUsesResolvedSHA(t *testing.T) {
	e := newEnv(t)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")
	head := "3333333333333333333333333333333333333333"

	rr := deliverBody(t, e.h, "release", "d-2", releaseBody("v2.0.0", "main"))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	arts, err := e.st.ArtifactsBySourceSHA(context.Background(), head)
	if err != nil {
		t.Fatalf("artifacts by source sha: %v", err)
	}
	var found bool
	for _, a := range arts {
		if a.Kind == "git_tag" && a.Version == "v2.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no git_tag artifact for v2.0.0 at %s; got %+v", head, arts)
	}
}

// TestReleaseFrontierNarrowsToTaggedCommit: a release whose target_commitish
// resolves to a known main commit covers only up to that commit, so a task
// that landed after it stays put until a later release reaches it.
func TestReleaseFrontierNarrowsToTaggedCommit(t *testing.T) {
	e := newEnv(t)
	e.setDoneState(t, demoRepo, "released")
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_merge.json")

	// Backport tag cut from the first commit on main; the task's work landed
	// at the third.
	const oldSHA = "1111111111111111111111111111111111111111"
	const headSHA = "3333333333333333333333333333333333333333"
	rr := deliverBody(t, e.h, "release", "d-3", releaseBody("v0.9.0", oldSHA))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("backport release: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got, want := e.rawQueryInt(t,
		`SELECT main_id FROM release_frontiers WHERE repo = $1 AND tag = 'v0.9.0'`, demoRepo),
		e.mainCommitID(t, oldSHA); got != want {
		t.Fatalf("backport frontier main_id = %d, want %d (the tagged commit)", got, want)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state after backport release = %q, want merged (not covered by the tag)", st)
	}

	// A release that does reach the task's commit delivers it.
	rr = deliverBody(t, e.h, "release", "d-4", releaseBody("v1.0.0", headSHA))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("head release: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if st := e.taskState(t, taskID); st != "released" {
		t.Fatalf("task state after head release = %q, want released", st)
	}
}

// TestReleaseDoesNotAdvanceNonReleaseRepo pins the done_state gate at the
// handler level: a release still records its frontier, but only a repo whose
// done_state is "released" lets tasks reach the released state.
func TestReleaseDoesNotAdvanceNonReleaseRepo(t *testing.T) {
	for _, doneState := range []string{"merged", "deployed_prod"} {
		t.Run(doneState, func(t *testing.T) {
			e := newEnv(t)
			e.setDoneState(t, demoRepo, doneState)
			taskID := e.seedTask(t)
			e.claimTask(t, taskID)
			deliverPushOK(t, e, "d-1", "push_branch.json")
			deliverPushOK(t, e, "d-2", "push_main_merge.json")

			deliverOK(t, e, "release", "d-3", "release_published.json")

			if st := e.taskState(t, taskID); st != "merged" {
				t.Fatalf("task state = %q, want merged (released gated on done_state)", st)
			}
			if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM release_frontiers`); n != 1 {
				t.Fatalf("release_frontiers rows = %d, want 1 (frontier is recorded regardless)", n)
			}
		})
	}
}

// TestRegistryPackagePublished: a container push mints a docker_image
// artifact keyed by image name and tag, carrying the OCI digest and the
// commit it was built from.
func TestRegistryPackagePublished(t *testing.T) {
	e := newEnv(t)
	deliverOK(t, e, "registry_package", "d-1", "registry_package_published.json")

	a, err := e.st.FindArtifactByImage(context.Background(),
		"ghcr.io/sunstoneinstitute/demo:v1.2.3")
	if err != nil {
		t.Fatalf("find artifact by image: %v", err)
	}
	if a.Kind != "docker_image" {
		t.Fatalf("kind = %q, want docker_image", a.Kind)
	}
	if a.Digest == nil || *a.Digest != "sha256:feed0000000000000000000000000000000000000000000000000000000000ff" {
		t.Fatalf("digest = %v, want the sha256 digest", a.Digest)
	}
	if a.SourceSHA != "abc1230000000000000000000000000000000000" {
		t.Fatalf("source_sha = %q, want the target commitish", a.SourceSHA)
	}
	if a.Repo != "sunstoneinstitute/demo" {
		t.Fatalf("repo = %q", a.Repo)
	}
	if a.BuiltAt.IsZero() {
		t.Fatal("built_at is zero")
	}
}

// TestRegistryPackageWithoutPackageURL: with no package_url the name is
// reconstructed as a GHCR reference, so it still matches the image reference a
// Kubernetes manifest carries. The bare package name never would.
func TestRegistryPackageWithoutPackageURL(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "published",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"registry_package": {
			"name": "demo",
			"package_type": "CONTAINER",
			"package_version": {
				"version": "sha256:feed",
				"container_metadata": {"tag": {"name": "v1.2.3"}}
			}
		}
	}`)
	rr := deliverBody(t, e.h, "registry_package", "d-1", body)
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	a, err := e.st.FindArtifactByImage(context.Background(),
		"ghcr.io/sunstoneinstitute/demo:v1.2.3")
	if err != nil {
		t.Fatalf("find artifact by image: %v", err)
	}
	if a.Kind != "docker_image" {
		t.Fatalf("kind = %q, want docker_image", a.Kind)
	}
}

// TestRegistryPackageUntaggedIsRecordedNotApplied: a package version with no
// container tag has no (name, version) key to store under, so it is recorded
// as an event with no artifact rather than failing the delivery.
func TestRegistryPackageUntaggedIsRecordedNotApplied(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "published",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"registry_package": {
			"name": "demo",
			"package_type": "CONTAINER",
			"package_version": {
				"version": "sha256:beef",
				"package_url": "ghcr.io/sunstoneinstitute/demo"
			}
		}
	}`)
	rr := deliverBody(t, e.h, "registry_package", "d-1", body)
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM artifacts`); n != 0 {
		t.Fatalf("artifacts = %d, want 0", n)
	}
}

// TestRegistryPackageNonContainerIgnored: only container packages become
// docker_image artifacts; a npm/nuget package version is recorded and
// otherwise ignored.
func TestRegistryPackageNonContainerIgnored(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "published",
		"repository": {"full_name": "sunstoneinstitute/demo"},
		"registry_package": {
			"name": "demo",
			"package_type": "NPM",
			"package_version": {"version": "1.0.0"}
		}
	}`)
	rr := deliverBody(t, e.h, "registry_package", "d-1", body)
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM artifacts`); n != 0 {
		t.Fatalf("artifacts = %d, want 0", n)
	}
}

func TestUnmappedRepoIgnored(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "opened",
		"repository": {"full_name": "other/repo"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`)
	rr := deliverBody(t, e.h, "issues", "d-1", body)
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ignored" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if typ := e.eventType(t, "d-1"); typ != "issues.opened.ignored" {
		t.Fatalf("event type = %q, want issues.opened.ignored", typ)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM issues`); n != 0 {
		t.Fatalf("issue rows = %d, want 0", n)
	}
}

func TestUnknownEventRecorded(t *testing.T) {
	e := newEnv(t)
	rr := deliverBody(t, e.h, "ping", "d-1", []byte(`{"zen": "Keep it simple."}`))
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if typ := e.eventType(t, "d-1"); typ != "ping" {
		t.Fatalf("event type = %q, want ping", typ)
	}
}

func TestOversizedBody413(t *testing.T) {
	e := newEnv(t)
	body := bytes.Repeat([]byte("a"), 5<<20+1)
	rr := deliverBody(t, e.h, "issues", "d-big", body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
	if n := e.eventCount(t); n != 0 {
		t.Fatalf("events recorded for oversized body = %d, want 0", n)
	}
}

// TestMountedOnServer proves server.go routes POST /hooks/github to the
// handler without bearer auth (the HMAC is the auth).
func TestMountedOnServer(t *testing.T) {
	e := newEnv(t)
	h, _, err := api.NewServer(e.st, api.Config{GitHubWebhookSecret: testSecret})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rr := deliver(t, h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestAppliedAtMarksMappedDeliveries: a mapped repo's delivery gets
// applied_at (even for an event type with no typed-table effect); an
// unmapped repo's .ignored delivery does not.
func TestAppliedAtMarksMappedDeliveries(t *testing.T) {
	e := newEnv(t)

	rr := deliver(t, e.h, "issues", "d-applied", "issues_opened.json")
	if rr.Code != http.StatusOK || ackStatus(t, rr) != "ok" {
		t.Fatalf("mapped delivery: %d %s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-applied' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("mapped delivery applied_at set = %d rows, want 1", n)
	}

	// "ping" routes to a nil apply but the repo is mapped: still marked, so
	// it never shows up as awaiting replay.
	rr = deliverBody(t, e.h, "ping", "d-ping", []byte(`{"repository":{"full_name":"sunstoneinstitute/demo"}}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("ping delivery: %d %s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-ping' AND applied_at IS NOT NULL`); n != 1 {
		t.Fatalf("nil-apply delivery applied_at set = %d rows, want 1", n)
	}

	rr = deliverBody(t, e.h, "issues", "d-ignored", []byte(`{
		"action": "opened",
		"repository": {"full_name": "other/repo"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("ignored delivery: %d %s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM events WHERE external_id = 'd-ignored' AND applied_at IS NULL`); n != 1 {
		t.Fatalf("ignored delivery applied_at NULL = %d rows, want 1", n)
	}
}

func TestHandledEventsMatchesApplyFunc(t *testing.T) {
	want := map[string]bool{
		"issues": true, "push": true, "pull_request": true, "deployment_status": true,
		"pull_request_review": true, "workflow_run": true, "release": true,
		"registry_package": true,
	}
	got := hooks.HandledEvents()
	if len(got) != len(want) {
		t.Fatalf("HandledEvents() = %v, want %d entries", got, len(want))
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected event %q", e)
		}
	}
}

// TestPROpenedMaterializesAwaitingApproval: 029 §7.1 — the requirement is a
// row, not an absence, so a task-correlated PR opening leaves an awaiting
// approval bound to its head sha, naming the requested reviewer when that
// login maps to an actor. A redelivery conflicts and writes nothing.
func TestPROpenedMaterializesAwaitingApproval(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	e.seedReviewer(t, "bob-actor", "bob")

	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened.json")

	state, requiredActor, resolvingActor, resolvedAt := e.approvalRow(t, 42)
	if state != "awaiting" {
		t.Errorf("approval state = %q, want awaiting", state)
	}
	if requiredActor == nil || *requiredActor != "bob-actor" {
		t.Errorf("required_actor = %v, want bob-actor", requiredActor)
	}
	if resolvingActor != nil || resolvedAt != nil {
		t.Errorf("open approval has resolving_actor %v / resolved_at %v, want both NULL",
			resolvingActor, resolvedAt)
	}
	if rev := e.rawQueryString(t,
		`SELECT subject_revision FROM approvals WHERE entity_id = $1`,
		store.PREntityID(demoRepo, 42)); rev != prHeadSHA {
		t.Errorf("subject_revision = %q, want the head sha %q", rev, prHeadSHA)
	}
	// The PR author is what the self-approval check compares against.
	if author := e.rawQueryString(t,
		`SELECT author FROM pull_requests WHERE repo = $1 AND number = 42`, demoRepo); author != "alice" {
		t.Errorf("pull_requests.author = %q, want alice", author)
	}

	deliverOK(t, e, "pull_request", "d-appr-2", "pull_request_opened.json")
	if n := e.approvalCount(t); n != 1 {
		t.Errorf("approval rows after redelivery = %d, want 1", n)
	}
}

// TestReadyForReviewAfterHeadMoveKeepsOneApproval: a draft whose head sha
// moves between opened and ready_for_review must not leave two open rows —
// a review resolves only one, and the other would sit on /reviews forever.
func TestReadyForReviewAfterHeadMoveKeepsOneApproval(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened.json")
	deliverOK(t, e, "pull_request", "d-appr-2", "pull_request_ready_for_review.json")

	if n := e.approvalCount(t); n != 1 {
		t.Fatalf("approval rows after a head move = %d, want 1", n)
	}
	if rev := e.rawQueryString(t,
		`SELECT subject_revision FROM approvals WHERE entity_id = $1`,
		store.PREntityID(demoRepo, 42)); rev != prHeadSHA {
		t.Errorf("subject_revision = %q, want the first-bound head sha %q", rev, prHeadSHA)
	}
}

// TestPROpenedUncorrelatedWritesNoApproval: a PR that names no task has no
// task to hold up, and failing to correlate must never fail the delivery.
func TestPROpenedUncorrelatedWritesNoApproval(t *testing.T) {
	e := newEnv(t)
	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened_uncorrelated.json")
	if n := e.approvalCount(t); n != 0 {
		t.Errorf("approval rows for an uncorrelated PR = %d, want 0", n)
	}
}

// TestReviewApprovedResolvesApproval: the review decides the open row, at the
// review's own submitted_at, attributed to the reviewer's actor.
func TestReviewApprovedResolvesApproval(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	e.seedReviewer(t, "bob-actor", "bob")
	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened.json")

	deliverOK(t, e, "pull_request_review", "d-rev-1", "pull_request_review_submitted.json")

	state, _, resolvingActor, resolvedAt := e.approvalRow(t, 42)
	if state != "approved" {
		t.Errorf("approval state = %q, want approved", state)
	}
	if resolvingActor == nil || *resolvingActor != "bob-actor" {
		t.Errorf("resolving_actor = %v, want bob-actor", resolvingActor)
	}
	want := time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC)
	if resolvedAt == nil || !resolvedAt.Equal(want) {
		t.Errorf("resolved_at = %v, want the review submitted_at %v", resolvedAt, want)
	}
}

// TestReviewCommentedLeavesApprovalAwaiting: a comment is not a decision.
// The row stays visible as waiting on someone (029 §7.1).
func TestReviewCommentedLeavesApprovalAwaiting(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened.json")

	deliverOK(t, e, "pull_request_review", "d-rev-1", "pull_request_review_commented.json")

	state, _, resolvingActor, resolvedAt := e.approvalRow(t, 42)
	if state != "awaiting" || resolvingActor != nil || resolvedAt != nil {
		t.Errorf("after a commented review: state=%q resolving_actor=%v resolved_at=%v, want awaiting/NULL/NULL",
			state, resolvingActor, resolvedAt)
	}
}

// TestChangesRequestedThenReviewRequestedReopens: 029 §7.1's re-request edge.
// The reviewer is unknown when the PR opens and only becomes an actor later,
// so the re-request also fills the required_actor the open ingest could not.
func TestChangesRequestedThenReviewRequestedReopens(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliverOK(t, e, "pull_request", "d-appr-1", "pull_request_opened.json")
	if _, requiredActor, _, _ := e.approvalRow(t, 42); requiredActor != nil {
		t.Fatalf("required_actor = %v with no matching actor, want NULL", requiredActor)
	}

	deliverOK(t, e, "pull_request_review", "d-rev-1", "pull_request_review_changes_requested.json")
	state, _, resolvingActor, _ := e.approvalRow(t, 42)
	if state != "changes_requested" {
		t.Fatalf("approval state = %q, want changes_requested", state)
	}
	if resolvingActor != nil {
		t.Errorf("resolving_actor = %v for an unmapped reviewer, want NULL", resolvingActor)
	}

	e.seedReviewer(t, "bob-actor", "bob")
	deliverOK(t, e, "pull_request", "d-appr-3", "pull_request_review_requested.json")

	state, requiredActor, resolvingActor, resolvedAt := e.approvalRow(t, 42)
	if state != "awaiting" || resolvingActor != nil || resolvedAt != nil {
		t.Errorf("after review_requested: state=%q resolving_actor=%v resolved_at=%v, want awaiting/NULL/NULL",
			state, resolvingActor, resolvedAt)
	}
	if requiredActor == nil || *requiredActor != "bob-actor" {
		t.Errorf("required_actor = %v, want bob-actor", requiredActor)
	}
	if n := e.approvalCount(t); n != 1 {
		t.Errorf("approval rows = %d, want 1 (reopen, not a second row)", n)
	}
}
