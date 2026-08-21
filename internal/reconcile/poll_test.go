package reconcile_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/reconcile"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	headSHA    = "1111111111111111111111111111111111111111"
	mergeSHA   = "2222222222222222222222222222222222222222"
	otherSHA   = "3333333333333333333333333333333333333333"
	releaseSHA = "4444444444444444444444444444444444444444"

	// Two commits landing in one run whose commit-date order is the reverse
	// of their sha order. See TestPollAppendsMainCommitsInCommitDateOrder.
	lateCommitSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // lower sha, later date
	earlyCommitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" // higher sha, earlier date
)

// mergeCommittedAt is the fake merge commit's committer date; poll writes it
// to main_commits.pushed_at.
var (
	mergeCommittedAt = time.Date(2026, 7, 20, 10, 5, 0, 0, time.UTC)
	earlyCommittedAt = time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	lateCommittedAt  = time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	// staleUpdatedAt predates the fake PR's updated_at (2026-07-20T10:00:00Z).
	staleUpdatedAt = time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
)

// compareBody is a compare-API response saying sha is on the branch, with the
// base commit's committer date attached.
func compareBody(at time.Time) string {
	return `{"status": "ahead", "base_commit": {"commit": {"committer": {"date": "` +
		at.Format(time.RFC3339) + `"}}}}`
}

func newFakeGitHub(t *testing.T, routes map[string]string) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/app/installation":
			io.WriteString(w, `{"id": 42}`)
		case r.URL.Path == "/app/installations/42/access_tokens":
			io.WriteString(w, `{"token": "inst-token"}`)
		default:
			body, ok := routes[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"message":"not found"}`)
				return
			}
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &githubauth.AppAuth{AppID: "1", Key: key, BaseURL: srv.URL}
}

// seedStaleTask: the backbone believes the PR is open (task in_review), but
// GitHub will report it merged onto main — ingestion was down for the
// pull_request.closed and push webhooks.
//
// The head ref is the task's own branch name: UpsertPR correlates through
// store.TaskIDFromRef, whose pattern is anchored, so a prefixed ref would
// correlate to nothing.
func seedStaleTask(t *testing.T, st *store.Store) (taskID string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}
	// state_log.event_id is a NOT NULL FK to events, so the seed transitions
	// run under a real seed event.
	if _, _, err := st.RecordEvent(ctx, "cli", "seed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			task, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: "demo", Title: "fix crash", Priority: "medium", Kind: "bug",
			}, eventID)
			if err != nil {
				return err
			}
			taskID = task.ID
			if err := store.Transition(tx, now, taskID, "ready", "in_progress", eventID); err != nil {
				return err
			}
			if err := store.Transition(tx, now, taskID, "in_progress", "in_review", eventID); err != nil {
				return err
			}
			_, err = store.UpsertPR(tx, store.PullRequest{
				Repo: "acme/app", Number: 12, Title: "fix", State: "open",
				HeadRef: taskID + "-fix", HeadSHA: headSHA,
				URL: "u", OpenedAt: now,
				// Older than the fake PR's updated_at, so UpsertPR's
				// non-regressing guard only lets the repair land because
				// gatherRepo threads GitHub's updated_at through. A NULL here
				// would pass the guard either way.
				UpdatedAt: staleUpdatedAt,
			}, "")
			return err
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return taskID
}

func mergedPRRoutes(taskID string) map[string]string {
	return map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "closed", "merged": true,
			"body": "", "html_url": "u",
			"merge_commit_sha": "` + mergeSHA + `",
			"merged_at": "2026-07-20T10:00:00Z", "created_at": "2026-07-19T09:00:00Z",
			"updated_at": "2026-07-20T10:00:00Z",
			"head": {"ref": "` + taskID + `-fix", "sha": "` + headSHA + `"}
		}`,
		"/repos/acme/app/compare/" + mergeSHA + "...main": compareBody(mergeCommittedAt),
		"/repos/acme/app/compare/" + headSHA + "...main":  `{"status": "diverged"}`,
		"/repos/acme/app/releases":                        `[]`,
	}
}

func taskState(t *testing.T, st *store.Store, taskID string) string {
	t.Helper()
	task, err := st.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task.State
}

func TestPollRepairsMergedWhileDown(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes(taskID))
	ctx := context.Background()

	res, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-1"})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if res.Candidates != 1 || len(res.Repaired) != 1 {
		t.Fatalf("result = %+v; want 1 candidate repaired", res)
	}
	if got := taskState(t, st, taskID); got != "merged" {
		t.Fatalf("task state = %q; want merged (repo done_state defaults to merged)", got)
	}
	// The merge commit is attributed to the task that owns the PR, not to the
	// repo's whole landed set.
	if got := res.Repaired[0].CommitsLanded; len(got) != 1 || got[0] != mergeSHA {
		t.Fatalf("commits landed = %v; want [%s]", got, mergeSHA)
	}
	// The stored PR row is repaired too. Unlike the task state (which the
	// commit correlation alone would advance), this write only survives
	// UpsertPR's non-regressing guard because gatherRepo threads GitHub's
	// updated_at through against the seeded, older one.
	prs, err := st.PRsForTask(ctx, taskID)
	if err != nil {
		t.Fatalf("PRs for task: %v", err)
	}
	if len(prs) != 1 || prs[0].State != "merged" || prs[0].MergeSHA == nil || *prs[0].MergeSHA != mergeSHA {
		t.Fatalf("stored PR = %+v; want state merged with merge_sha %s", prs, mergeSHA)
	}

	// The transition attributes to the reconcile.poll system event.
	entries, err := st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no state_log entries")
	}
	evs := eventByID(t, st, entries[len(entries)-1].EventID)
	if evs.Source != "system" || evs.Type != "reconcile.poll" || evs.ExternalID != "run-1" {
		t.Fatalf("attributed event = %+v; want the reconcile.poll run event", evs)
	}

	// Convergence: a second run records its run event but changes nothing.
	before := len(entries)
	if _, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-2"}); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	entries, err = st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != before {
		t.Fatalf("second run added %d state_log entries; want 0", len(entries)-before)
	}
}

// seedOpenTask adds a second task in the same repo whose PR is still open, so
// the run has two candidates in one repo.
func seedOpenTask(t *testing.T, st *store.Store) (taskID string) {
	t.Helper()
	if _, _, err := st.RecordEvent(context.Background(), "cli", "seed2-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			task, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: "demo", Title: "other", Priority: "medium", Kind: "bug",
			}, eventID)
			if err != nil {
				return err
			}
			taskID = task.ID
			if err := store.Transition(tx, now, taskID, "ready", "in_progress", eventID); err != nil {
				return err
			}
			if err := store.Transition(tx, now, taskID, "in_progress", "in_review", eventID); err != nil {
				return err
			}
			_, err = store.UpsertPR(tx, store.PullRequest{
				Repo: "acme/app", Number: 13, Title: "other", State: "open",
				HeadRef: taskID + "-other", HeadSHA: otherSHA,
				URL: "u2", OpenedAt: now,
			}, "")
			return err
		}); err != nil {
		t.Fatalf("seed open task: %v", err)
	}
	return taskID
}

// A repo's landed shas are not a repo-wide broadcast: each task reports only
// the commits gathered on its own behalf.
func TestPollAttributesCommitsPerTask(t *testing.T) {
	st := store.OpenTestStore(t)
	merged := seedStaleTask(t, st)
	open := seedOpenTask(t, st)

	routes := mergedPRRoutes(merged)
	routes["/repos/acme/app/pulls/13"] = `{
		"number": 13, "title": "other", "state": "open", "merged": false,
		"body": "", "html_url": "u2",
		"created_at": "2026-07-19T09:00:00Z", "updated_at": "2026-07-19T09:00:00Z",
		"head": {"ref": "` + open + `-other", "sha": "` + otherSHA + `"}
	}`
	app := newFakeGitHub(t, routes)

	res, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-multi"})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if res.Candidates != 2 || len(res.Repaired) != 2 {
		t.Fatalf("result = %+v; want 2 candidates repaired", res)
	}
	byTask := map[string][]string{}
	for _, r := range res.Repaired {
		byTask[r.TaskID] = r.CommitsLanded
	}
	if got := byTask[merged]; len(got) != 1 || got[0] != mergeSHA {
		t.Fatalf("%s commits landed = %v; want [%s]", merged, got, mergeSHA)
	}
	if got := byTask[open]; len(got) != 0 {
		t.Fatalf("%s commits landed = %v; want none — those are %s's commits", open, got, merged)
	}
}

// seedLandedTask: a task already at merged whose only correlation is an
// already-landed task_commits row and no PR at all — PollCandidates'
// task_commits union arm (the local_merge/marker path). The repo is
// release-terminated, so only a release can still move this task.
func seedLandedTask(t *testing.T, st *store.Store) (taskID string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}
	if err := st.SetRepoDoneState(ctx, "acme/app", "released"); err != nil {
		t.Fatalf("set done_state: %v", err)
	}
	if _, _, err := st.RecordEvent(ctx, "cli", "seed-landed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			task, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: "demo", Title: "landed locally", Priority: "medium", Kind: "bug",
			}, eventID)
			if err != nil {
				return err
			}
			taskID = task.ID
			for _, hop := range [][2]string{
				{"ready", "in_progress"}, {"in_progress", "in_review"}, {"in_review", "merged"},
			} {
				if err := store.Transition(tx, now, taskID, hop[0], hop[1], eventID); err != nil {
					return err
				}
			}
			if _, err := store.AppendMainCommit(tx, "acme/app", releaseSHA, now); err != nil {
				return err
			}
			return store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: "acme/app", SHA: releaseSHA,
				Source: "local_merge", SeenAt: now,
			})
		}); err != nil {
		t.Fatalf("seed landed task: %v", err)
	}
	return taskID
}

// Releases are a repo-level fact, so the apply phase must run on what was
// gathered rather than on whether any task-level repair was detected: this
// candidate yields no PR and no newly-landed commit, and a release published
// during the outage still has to move it to released (013 §2.2).
func TestPollAppliesReleaseWithoutTaskLevelRepair(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedLandedTask(t, st)
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
		"/repos/acme/app/releases": `[{
			"tag_name": "v1.2.0", "target_commitish": "` + releaseSHA + `",
			"published_at": "2026-07-21T10:00:00Z"
		}]`,
	})
	ctx := context.Background()

	res, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-rel"})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if res.Candidates != 1 {
		t.Fatalf("candidates = %d; want 1", res.Candidates)
	}
	// The premise: nothing task-level to repair. If this ever stops holding,
	// the test no longer covers the gate.
	if len(res.Repaired) != 0 {
		t.Fatalf("repaired = %+v; want none — the gap is the no-repair path", res.Repaired)
	}
	if got := taskState(t, st, taskID); got != "released" {
		t.Fatalf("task state = %q; want released (done_state=released, release covers the commit)", got)
	}

	// Convergence: re-running changes nothing.
	entries, err := st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	before := len(entries)
	if _, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-rel-2"}); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	entries, err = st.StateLogForEntity(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != before {
		t.Fatalf("second run added %d state_log entries; want 0", len(entries)-before)
	}
}

func TestPollDryRunReportsWithoutWriting(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes(taskID))

	res, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-dry", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run poll: %v", err)
	}
	if !res.DryRun || len(res.Repaired) != 1 {
		t.Fatalf("dry-run result = %+v; want the same 1 repair reported", res)
	}
	if got := taskState(t, st, taskID); got != "in_review" {
		t.Fatalf("dry-run advanced the task to %q; want untouched in_review", got)
	}
}

// seedTwoCommitTask seeds a task correlated only through two unlanded
// task_commits rows (no PR), so one poll run appends two main_commits.
func seedTwoCommitTask(t *testing.T, st *store.Store) (taskID string) {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}
	if _, _, err := st.RecordEvent(ctx, "cli", "seed-two-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			task, err := store.CreateTask(tx, now, store.TaskInput{
				ProjectID: "demo", Title: "two commits", Priority: "medium", Kind: "bug",
			}, eventID)
			if err != nil {
				return err
			}
			taskID = task.ID
			for _, hop := range [][2]string{{"ready", "in_progress"}, {"in_progress", "in_review"}} {
				if err := store.Transition(tx, now, taskID, hop[0], hop[1], eventID); err != nil {
					return err
				}
			}
			for _, sha := range []string{lateCommitSHA, earlyCommitSHA} {
				if err := store.InsertTaskCommit(tx, store.TaskCommit{
					TaskID: taskID, Repo: "acme/app", SHA: sha,
					Source: "local_merge", SeenAt: now,
				}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
		t.Fatalf("seed two-commit task: %v", err)
	}
	return taskID
}

// mainCommit reads one main_commits row (id is the permanent per-repo
// ordering seq) through a throwaway event's transaction.
func mainCommit(t *testing.T, st *store.Store, sha string) (id int64, pushedAt time.Time) {
	t.Helper()
	if _, _, err := st.RecordEvent(context.Background(), "cli", "read-"+t.Name()+"-"+sha, "test.read", nil,
		func(tx *sql.Tx, _ int64) error {
			return tx.QueryRow(
				`SELECT id, pushed_at FROM main_commits WHERE repo = $1 AND sha = $2`,
				"acme/app", sha).Scan(&id, &pushedAt)
		}); err != nil {
		t.Fatalf("read main_commit %s: %v", sha, err)
	}
	return id, pushedAt.UTC()
}

// main_commits.id is the permanent per-repo ordering every frontier
// comparison reads (covered(), TasksBelowFrontier). A run that lands more
// than one commit must append them in the order they landed: here the two
// shas' commit-date order is the reverse of their sha order, so appending in
// sha order would give the later commit the lower id and let a release cut
// before it read as covering it.
func TestPollAppendsMainCommitsInCommitDateOrder(t *testing.T) {
	st := store.OpenTestStore(t)
	seedTwoCommitTask(t, st)
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
		"/repos/acme/app/compare/" + lateCommitSHA + "...main":  compareBody(lateCommittedAt),
		"/repos/acme/app/compare/" + earlyCommitSHA + "...main": compareBody(earlyCommittedAt),
		"/repos/acme/app/releases":                              `[]`,
	})

	if _, err := reconcile.Poll(context.Background(), st, app, reconcile.Options{RunID: "run-order"}); err != nil {
		t.Fatalf("poll: %v", err)
	}
	earlyID, earlyPushed := mainCommit(t, st, earlyCommitSHA)
	lateID, latePushed := mainCommit(t, st, lateCommitSHA)
	if earlyID >= lateID {
		t.Fatalf("main_commits ids = early %d, late %d; want the earlier commit to have the lower id "+
			"(sha order would invert it: %s < %s)", earlyID, lateID, lateCommitSHA, earlyCommitSHA)
	}
	// pushed_at records when the commit landed, not when reconcile noticed.
	if !earlyPushed.Equal(earlyCommittedAt) || !latePushed.Equal(lateCommittedAt) {
		t.Fatalf("pushed_at = %v, %v; want %v, %v", earlyPushed, latePushed, earlyCommittedAt, lateCommittedAt)
	}
}

// RecordEvent skips apply on a duplicate (source, external_id), so a reused
// run id would return a fully populated report describing writes that never
// happened. Both a reused and an empty run id must be errors.
func TestPollRejectsReusedAndEmptyRunID(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes(taskID))
	ctx := context.Background()

	if _, err := reconcile.Poll(ctx, st, app, reconcile.Options{}); err == nil {
		t.Fatal("poll with an empty run id succeeded; want an error")
	}
	if _, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-dup"}); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	_, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-dup"})
	if err == nil {
		t.Fatal("poll with a reused run id succeeded; want an error naming it")
	}
	if !strings.Contains(err.Error(), "run-dup") {
		t.Fatalf("error = %v; want it to name the duplicate run id", err)
	}
}

// TestPollSetsPRAuthorEnablingSelfApprovalRefusal reproduces WL-244: a PR
// first seen through the reconcile poll must carry its author immediately,
// not only after a later webhook fills it in. While author is unset,
// store.IsSelfApproval cannot prove anything either way, so the approver who
// authored the PR could self-approve it during that window (029 §7.1's
// default refusal). The second half — actually deciding through
// store.DecideApproval — is what catches a fix that sets Author but leaves
// it unused.
func TestPollSetsPRAuthorEnablingSelfApprovalRefusal(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	routes := map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "open",
			"body": "", "html_url": "u",
			"created_at": "2026-07-19T09:00:00Z",
			"updated_at": "2026-07-21T10:00:00Z",
			"head": {"ref": "` + taskID + `-fix", "sha": "` + headSHA + `"},
			"user": {"login": "octo"}
		}`,
		"/repos/acme/app/releases": `[]`,
	}
	app := newFakeGitHub(t, routes)
	ctx := context.Background()

	if _, err := reconcile.Poll(ctx, st, app, reconcile.Options{RunID: "run-author"}); err != nil {
		t.Fatalf("poll: %v", err)
	}

	pr, err := st.GetPR(ctx, "acme/app", 12)
	if err != nil {
		t.Fatalf("get pr: %v", err)
	}
	if pr.TaskID == nil || *pr.TaskID != taskID {
		t.Fatalf("pr task_id = %v, want %s — the seeded head_ref must correlate for this test to mean anything", pr.TaskID, taskID)
	}
	if pr.Author != "octo" {
		t.Fatalf("author = %q, want octo — the poll engine must capture the PR author so self-approval can be refused before any webhook arrives", pr.Author)
	}

	// Materialize an approval on this PR exactly as the webhook review
	// ingest would, then prove the author it names cannot decide it.
	if err := st.UpsertHumanActor(ctx, "octo-actor", "Octo", false, "octo", "", nil); err != nil {
		t.Fatalf("upsert actor: %v", err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return store.InsertAwaitingApproval(tx, st.Now(), "pr",
			store.PREntityID("acme/app", 12), headSHA, nil, nil)
	}); err != nil {
		t.Fatalf("insert awaiting approval: %v", err)
	}
	rows, err := st.ListAwaitingApprovals(ctx)
	if err != nil {
		t.Fatalf("list awaiting approvals: %v", err)
	}
	var approvalID int64
	for _, row := range rows {
		if row.EntityID == store.PREntityID("acme/app", 12) {
			approvalID = row.ID
		}
	}
	if approvalID == 0 {
		t.Fatalf("seeded approval not found in the awaiting queue")
	}

	err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := store.DecideApproval(tx, store.DecideInput{
			ApprovalID: approvalID, Decision: "approve", ActorID: "octo-actor", Now: st.Now(),
		})
		return err
	})
	if !errors.Is(err, store.ErrSelfApproval) {
		t.Fatalf("decide by the PR's own author = %v, want ErrSelfApproval", err)
	}
}

// eventByID reads one event row for attribution assertions.
func eventByID(t *testing.T, st *store.Store, id int64) store.Event {
	t.Helper()
	ev, err := st.GetEvent(context.Background(), id)
	if err != nil {
		t.Fatalf("event %d: %v", id, err)
	}
	return ev
}
