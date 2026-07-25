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

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/hooks"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const testSecret = "test-webhook-secret"

// env is a webhook test fixture: a real store with repo
// sunstoneinstitute/demo mapped to project "demo" and the GitHub handler.
// Raw SQL assertions go through the store's own connection pool.
type env struct {
	st *store.Store
	h  http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := store.OpenTestStore(t)

	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "sunstoneinstitute/demo"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	return &env{
		st: st,
		h:  hooks.NewGitHubHandler(st, testSecret, slog.Default()),
	}
}

// seedTask creates a task in project "demo" (state ready) and returns its id.
func (e *env) seedTask(t *testing.T) string {
	t.Helper()
	var id string
	err := e.st.Tx(context.Background(), func(tx *sql.Tx) error {
		task, err := store.CreateTask(tx, e.st.Now(), store.TaskInput{
			ProjectID: "demo", Title: "fix crash", Priority: "medium", Kind: "bug",
		})
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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

func status(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return m["status"]
}

// rawQueryInt runs a single-value SQL query against the store's database.
func (e *env) rawQueryInt(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.st.DBForTests().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("raw query %q: %v", query, err)
	}
	return n
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
	h := hooks.NewGitHubHandler(e.st, "", slog.Default())
	rr := deliver(t, h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestIdempotency(t *testing.T) {
	e := newEnv(t)
	rr := deliver(t, e.h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("first delivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = deliver(t, e.h, "issues", "d-1", "issues_opened.json")
	if rr.Code != http.StatusOK || status(t, rr) != "duplicate" {
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
	issues, err := e.st.ListIssues(ctx, "")
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
	issues, err = e.st.ListIssues(ctx, "")
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	pr, err := e.st.GetPR(ctx, "sunstoneinstitute/demo", 42)
	if err != nil {
		t.Fatalf("get PR: %v", err)
	}
	if pr.TaskID == nil || *pr.TaskID != taskID {
		t.Fatalf("PR task id = %v, want %s", pr.TaskID, taskID)
	}
	if pr.State != "open" || pr.HeadRef != "wl/WL-1-x" || pr.MergeSHA != nil ||
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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

func TestPRMergedMovesTaskToMerged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)
	deliver(t, e.h, "pull_request", "d-1", "pull_request_opened.json") // → in_review

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

	pr, err := e.st.GetPR(ctx, "sunstoneinstitute/demo", 42)
	if err != nil {
		t.Fatalf("get PR: %v", err)
	}
	if pr.State != "merged" || pr.MergeSHA == nil || pr.MergedAt == nil {
		t.Fatalf("PR after merge = %+v", pr)
	}
	if _, err := e.st.ActiveLease(ctx, taskID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active lease err = %v, want ErrNotFound (lease closed)", err)
	}
	task, err := e.st.GetTask(ctx, taskID)
	if err != nil || task.State != "merged" {
		t.Fatalf("task = %+v (err %v), want state merged", task, err)
	}
}

func TestReviewSubmitted(t *testing.T) {
	e := newEnv(t)
	rr := deliver(t, e.h, "pull_request_review", "d-1", "pull_request_review_submitted.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	arts, err := e.st.ArtifactsBySourceSHA(context.Background(),
		"abc1230000000000000000000000000000000000")
	if err != nil || len(arts) != 1 {
		t.Fatalf("artifacts = %v, err = %v, want 1", arts, err)
	}
	a := arts[0]
	if a.Kind != "git_tag" || a.Name != "sunstoneinstitute/demo" || a.Version != "v1.2.3" ||
		a.Repo != "sunstoneinstitute/demo" || a.BuiltAt.IsZero() {
		t.Fatalf("artifact = %+v", a)
	}
	// No main commit has ever been seen for the repo: there is nothing for
	// the release to cover, so no frontier is recorded.
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM release_frontiers`); n != 0 {
		t.Fatalf("release_frontiers rows = %d, want 0", n)
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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

func TestUnmappedRepoIgnored(t *testing.T) {
	e := newEnv(t)
	body := []byte(`{
		"action": "opened",
		"repository": {"full_name": "other/repo"},
		"issue": {"number": 1, "title": "x", "state": "open", "html_url": "u"}
	}`)
	rr := deliverBody(t, e.h, "issues", "d-1", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ignored" {
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
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
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}
