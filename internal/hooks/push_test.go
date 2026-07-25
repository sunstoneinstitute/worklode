package hooks_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

const demoRepo = "sunstoneinstitute/demo"

// taskCommitSource returns the source recorded for one (task, repo, sha)
// attribution, or "" if there is no such row.
func (e *env) taskCommitSource(t *testing.T, taskID, repo, sha string) string {
	t.Helper()
	var src string
	err := e.st.DBForTests().QueryRow(
		`SELECT source FROM task_commits WHERE task_id = $1 AND repo = $2 AND sha = $3`,
		taskID, repo, sha).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("task_commit source for %s/%s: %v", taskID, sha, err)
	}
	return src
}

// deliverPushOK posts a push fixture and requires a clean "ok" — an apply
// that errors rolls its whole transaction back, which otherwise looks
// indistinguishable from "the handler correctly did nothing".
func deliverPushOK(t *testing.T, e *env, deliveryID, fixtureFile string) {
	t.Helper()
	rr := deliver(t, e.h, "push", deliveryID, fixtureFile)
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("deliver %s (%s): code=%d body=%s", fixtureFile, deliveryID, rr.Code, rr.Body.String())
	}
}

// mainCommitID returns the per-repo ordering id recorded for sha.
func (e *env) mainCommitID(t *testing.T, sha string) int {
	t.Helper()
	return e.rawQueryInt(t,
		`SELECT id FROM main_commits WHERE repo = $1 AND sha = $2`, demoRepo, sha)
}

// seedTaskInProject creates a second project with its own task-id key and
// one ready task in it, returning the task id (e.g. "SW-1"). Its repo is
// still sunstoneinstitute/demo: a marker names any project's task.
func (e *env) seedTaskInProject(t *testing.T, projectID, key string) string {
	t.Helper()
	if err := e.st.CreateProject(context.Background(), projectID, projectID, key); err != nil {
		t.Fatalf("create project %s: %v", projectID, err)
	}
	var id string
	err := e.st.Tx(context.Background(), func(tx *sql.Tx) error {
		task, err := store.CreateTask(tx, e.st.Now(), store.TaskInput{
			ProjectID: projectID, Title: "validate input", Priority: "medium", Kind: "bug",
		})
		if err != nil {
			return err
		}
		id = task.ID
		return nil
	})
	if err != nil {
		t.Fatalf("seed task in %s: %v", projectID, err)
	}
	return id
}

func (e *env) taskState(t *testing.T, taskID string) string {
	t.Helper()
	task, err := e.st.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task %s: %v", taskID, err)
	}
	return task.State
}

func TestPushBranchRecordsTaskCommits(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t) // WL-1
	e.claimTask(t, taskID)  // in_progress

	rr := deliver(t, e.h, "push", "d-1", "push_branch.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	for _, sha := range []string{
		"1111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222",
	} {
		if src := e.taskCommitSource(t, taskID, demoRepo, sha); src != "branch_push" {
			t.Fatalf("task_commit source for %s = %q, want branch_push", sha, src)
		}
	}
	// A branch push is not a landing: no main_commits, no state change.
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM main_commits`); n != 0 {
		t.Fatalf("main_commits rows = %d, want 0", n)
	}
	if st := e.taskState(t, taskID); st != "in_progress" {
		t.Fatalf("task state = %q, want in_progress", st)
	}
}

func TestPushMainMergeAdvancesTask(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	deliverPushOK(t, e, "d-1", "push_branch.json")
	rr := deliver(t, e.h, "push", "d-2", "push_main_merge.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM main_commits WHERE repo = $1`, demoRepo); n != 3 {
		t.Fatalf("main_commits rows = %d, want 3", n)
	}
	const mergeSHA = "3333333333333333333333333333333333333333"
	if src := e.taskCommitSource(t, taskID, demoRepo, mergeSHA); src != "merge_message" {
		t.Fatalf("merge task_commit source = %q, want merge_message", src)
	}
	// main_commits ids must follow payload order: the per-repo id is the
	// "seq" every frontier comparison in the resolver is built on.
	first := e.mainCommitID(t, "1111111111111111111111111111111111111111")
	second := e.mainCommitID(t, "2222222222222222222222222222222222222222")
	third := e.mainCommitID(t, mergeSHA)
	if !(first < second && second < third) {
		t.Fatalf("main_commits ids = %d, %d, %d; want strictly increasing in push order",
			first, second, third)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
}

func TestPushMainMarkerAdvancesTask(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t) // stays ready

	rr := deliver(t, e.h, "push", "d-1", "push_main_marker.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	const sha = "4444444444444444444444444444444444444444"
	if src := e.taskCommitSource(t, taskID, demoRepo, sha); src != "marker" {
		t.Fatalf("marker task_commit source = %q, want marker", src)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
}

// TestPushMainFastForwardAdvancesTask covers a rebase/fast-forward merge:
// the branch commits land on main verbatim, with no merge subject and no
// marker. Attribution then rests entirely on the commits recorded by the
// earlier branch push.
func TestPushMainFastForwardAdvancesTask(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	deliverPushOK(t, e, "d-1", "push_branch.json")
	deliverPushOK(t, e, "d-2", "push_main_ff.json")

	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM main_commits WHERE repo = $1`, demoRepo); n != 2 {
		t.Fatalf("main_commits rows = %d, want 2", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
}

// TestPushMainMarkerOtherProjectKey pins the marker rule: "WL-Task:" is a
// fixed label followed by any project's task key, not a WL- prefix match.
func TestPushMainMarkerOtherProjectKey(t *testing.T) {
	e := newEnv(t)
	e.seedTask(t) // WL-1, unrelated
	taskID := e.seedTaskInProject(t, "other", "SW")
	if taskID != "SW-1" {
		t.Fatalf("seeded task id = %q, want SW-1", taskID)
	}

	deliverPushOK(t, e, "d-1", "push_main_marker_other_key.json")

	const sha = "8888888888888888888888888888888888888888"
	if src := e.taskCommitSource(t, taskID, demoRepo, sha); src != "marker" {
		t.Fatalf("marker task_commit source = %q, want marker", src)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
	if st := e.taskState(t, "WL-1"); st != "ready" {
		t.Fatalf("unrelated task state = %q, want ready", st)
	}
}

func TestPushLastDeployMapsShas(t *testing.T) {
	e := newEnv(t)
	deliverPushOK(t, e, "d-1", "push_main_merge.json")

	rr := deliver(t, e.h, "push", "d-2", "push_last_deploy.json")
	if rr.Code != http.StatusOK || status(t, rr) != "ok" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	mainID := e.rawQueryInt(t,
		`SELECT id FROM main_commits WHERE repo = $1 AND sha = $2`,
		demoRepo, "3333333333333333333333333333333333333333")
	got := e.rawQueryInt(t,
		`SELECT main_id FROM deploy_shas WHERE repo = $1 AND sha = $2`,
		demoRepo, "5555555555555555555555555555555555555555")
	if got != mainID {
		t.Fatalf("deploy_sha main_id = %d, want %d", got, mainID)
	}
}

func TestPushUnmappedRepoIgnored(t *testing.T) {
	e := newEnv(t)
	e.seedTask(t)
	body := []byte(`{
		"ref": "refs/heads/lode/WL-1-add-widget",
		"repository": {"full_name": "other/repo", "default_branch": "main"},
		"commits": [{"id": "1111111111111111111111111111111111111111", "message": "Add widget"}]
	}`)
	rr := deliverBody(t, e.h, "push", "d-1", body)
	if rr.Code != http.StatusOK || status(t, rr) != "ignored" {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if typ := e.eventType(t, "d-1"); typ != "push.ignored" {
		t.Fatalf("event type = %q, want push.ignored", typ)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 0 {
		t.Fatalf("task_commit rows = %d, want 0", n)
	}
}

// TestPushUnrelatedRefsAreNoOps covers refs the lifecycle ignores: a branch
// that is neither a task branch, the default branch, nor last-deploy/*, and
// a tag push (no refs/heads/ prefix at all).
func TestPushUnrelatedRefsAreNoOps(t *testing.T) {
	e := newEnv(t)
	e.seedTask(t)

	bodies := map[string][]byte{
		"d-branch": []byte(`{
			"ref": "refs/heads/dependabot/npm_and_yarn/lodash-4.17.21",
			"repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"},
			"commits": [{"id": "6666666666666666666666666666666666666666", "message": "Bump lodash"}]
		}`),
		"d-tag": []byte(`{
			"ref": "refs/tags/v1",
			"repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"},
			"commits": [{"id": "7777777777777777777777777777777777777777", "message": "Merge branch 'lode/WL-1-add-widget'"}]
		}`),
	}
	for id, body := range bodies {
		rr := deliverBody(t, e.h, "push", id, body)
		if rr.Code != http.StatusOK || status(t, rr) != "ok" {
			t.Fatalf("%s: code=%d body=%s", id, rr.Code, rr.Body.String())
		}
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 0 {
		t.Fatalf("task_commit rows = %d, want 0", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM main_commits`); n != 0 {
		t.Fatalf("main_commit rows = %d, want 0", n)
	}
	if st := e.taskState(t, "WL-1"); st != "ready" {
		t.Fatalf("task state = %q, want ready", st)
	}
}

// TestPushRedeliveryIsIdempotent: GitHub redelivers with the same
// X-GitHub-Delivery; RecordEvent short-circuits before apply runs.
func TestPushRedeliveryIsIdempotent(t *testing.T) {
	e := newEnv(t)
	taskID := e.seedTask(t)
	e.claimTask(t, taskID)

	deliverPushOK(t, e, "d-1", "push_branch.json")
	rr := deliver(t, e.h, "push", "d-1", "push_branch.json")
	if rr.Code != http.StatusOK || status(t, rr) != "duplicate" {
		t.Fatalf("redelivery: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 2 {
		t.Fatalf("task_commit rows = %d, want 2", n)
	}

	// Same facts under a fresh delivery id: apply runs again and must be a
	// no-op on top of itself.
	deliverPushOK(t, e, "d-2", "push_main_merge.json")
	deliverPushOK(t, e, "d-3", "push_main_merge.json")
	if n := e.rawQueryInt(t,
		`SELECT COUNT(*) FROM main_commits WHERE repo = $1`, demoRepo); n != 3 {
		t.Fatalf("main_commit rows = %d, want 3", n)
	}
	if n := e.rawQueryInt(t, `SELECT COUNT(*) FROM task_commits`); n != 3 {
		t.Fatalf("task_commit rows = %d, want 3", n)
	}
	if st := e.taskState(t, taskID); st != "merged" {
		t.Fatalf("task state = %q, want merged", st)
	}
}
