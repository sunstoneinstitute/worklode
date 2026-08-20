package reconcile_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/reconcile"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

const (
	headSHA  = "1111111111111111111111111111111111111111"
	mergeSHA = "2222222222222222222222222222222222222222"
	otherSHA = "3333333333333333333333333333333333333333"
)

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
		"/repos/acme/app/compare/" + mergeSHA + "...main": `{"status": "ahead"}`,
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

// eventByID reads one event row for attribution assertions.
func eventByID(t *testing.T, st *store.Store, id int64) store.Event {
	t.Helper()
	ev, err := st.GetEvent(context.Background(), id)
	if err != nil {
		t.Fatalf("event %d: %v", id, err)
	}
	return ev
}
