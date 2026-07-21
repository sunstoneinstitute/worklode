package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

// bodyContains fails the test unless every want string appears in body,
// reporting all misses at once.
func bodyContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q\n--- body ---\n%s", w, body)
		}
	}
}

func TestBoardPageOrgBoard(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj1")
	createProject(t, st, "proj2")

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Leased task", "priority": "high", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim WL-1 status = %d, body %s", rr.Code, rr.Body.String())
	}

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Blocker task", "priority": "high", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj1", "title": "Blocked task", "priority": "medium", "kind": "bug",
	})
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token, map[string]any{"to": "WL-3", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add blocking edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	// A runtime failure, recorded directly through the store (as the
	// watcher would via POST /api/v1/runtime-events).
	seedEvent(t, st, "runtime-1", func(tx *sql.Tx, _ int64) error {
		_, err := store.InsertRuntimeEvent(tx, store.RuntimeEvent{
			Cluster: "prod-1", Kind: "crashloop", Workload: "app",
			Message: "CrashLoopBackOff on app", OccurredAt: st.Now(),
		})
		return err
	})

	// A fresh inbox issue.
	if err := st.AddRepo(context.Background(), "proj1", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	seedIssue(t, st, "acme/widgets", 1, "An untriaged issue")

	rr = doReq(t, h, "GET", "/", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board page status = %d, body %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body := rr.Body.String()
	bodyContains(t, body,
		"proj1", "proj2", // project names
		"Leased task", "alice", // in_progress task + holder actor
		"Blocked", "Blocked task", // the blocked bucket + the blocked task's title
		"CrashLoopBackOff on app", // recent-failures message
		"Inbox: 1 new issue",      // inbox count
	)
}

func TestTaskPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Add feature", "body": "do the thing", "priority": "high", "kind": "feature",
	})
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	const (
		repo     = "org/app"
		mergeSHA = "mergesha1"
	)
	seedEvent(t, st, "pr-open", func(tx *sql.Tx, _ int64) error {
		_, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "open",
			HeadRef: "wl/WL-1-add-feature", HeadSHA: "headsha1",
			URL: "https://github.com/org/app/pull/7", OpenedAt: st.Now(),
		}, "")
		return err
	})
	seedEvent(t, st, "pr-merge", func(tx *sql.Tx, _ int64) error {
		merged := st.Now()
		ms := mergeSHA
		_, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: 7, Title: "Add feature", State: "merged",
			HeadRef: "wl/WL-1-add-feature", HeadSHA: "headsha1", MergeSHA: &ms,
			URL: "https://github.com/org/app/pull/7", OpenedAt: st.Now(), MergedAt: &merged,
		}, "")
		return err
	})
	seedEvent(t, st, "artifact", func(tx *sql.Tx, _ int64) error {
		_, err := store.CreateArtifact(tx, store.Artifact{
			Kind: "docker_image", Name: "reg/app", Version: "1.2.3",
			Repo: repo, SourceSHA: mergeSHA, BuiltAt: st.Now(),
		})
		return err
	})

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	bodyContains(t, body,
		"WL-1", "Add feature", // id + title
		"in_progress",                // state
		"alice",                      // lease holder
		"do the thing",               // body
		"State change",               // timeline: state entry label
		"Pull request",               // timeline: pr entry label
		"org/app#7",                  // pr entry summary
		"Artifact",                   // timeline: artifact entry label
		"docker_image reg/app 1.2.3", // artifact entry summary
	)

	rr = doReq(t, h, "GET", "/tasks/WL-99", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task page status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

func TestProjectPage(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.AddRepo(context.Background(), "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scoped task", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "GET", "/projects/proj", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("project page status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	bodyContains(t, body, "proj", "acme/widgets", "Scoped task")

	rr = doReq(t, h, "GET", "/projects/nosuch", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown project page status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}
