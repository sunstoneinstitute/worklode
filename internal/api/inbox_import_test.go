package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// importGitHub serves the installation handshake plus one page each of issues
// and pulls, then empty pages.
func importGitHub(t *testing.T, issues, pulls []map[string]any) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets/issues":
			if page > 1 {
				json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			json.NewEncoder(w).Encode(issues)
		case "/repos/acme/widgets/pulls":
			if page > 1 {
				json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			json.NewEncoder(w).Encode(pulls)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// importServer builds a server with project "proj" mapped to acme/widgets.
func importServer(t *testing.T, app *githubauth.AppAuth) (*store.Store, func(body map[string]any) *httptest.ResponseRecorder) {
	t.Helper()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st.CreateProject(ctx, "proj", "Proj", "PR"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.AddRepo(ctx, "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	s := &server{st: st, cfg: Config{}, log: slog.Default(), appAuth: app}
	return st, func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/inbox/import", bytes.NewReader(b))
		rr := httptest.NewRecorder()
		s.importInbox(rr, req)
		return rr
	}
}

func openIssue(n int, title string) map[string]any {
	return map[string]any{"number": n, "title": title, "state": "open",
		"html_url": fmt.Sprintf("https://gh/%d", n), "updated_at": "2026-01-01T00:00:00Z"}
}

// countEvents returns the number of rows in the events table, via the
// store's cross-package test surface (DBForTests) — there is no production
// events counter, and the plan says not to add one just for this test.
func countEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DBForTests().QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func TestImportPopulatesInbox(t *testing.T) {
	app := importGitHub(t, []map[string]any{openIssue(1, "first"), openIssue(2, "second")}, nil)
	st, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "open"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got struct {
		Issues struct{ New, Updated int } `json:"issues"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Issues.New != 2 || got.Issues.Updated != 0 {
		t.Fatalf("counts = %+v, want new=2 updated=0", got.Issues)
	}
	issues, err := st.ListIssues(context.Background(), "new", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("stored %d issues, want 2", len(issues))
	}
}

func TestImportIsIdempotent(t *testing.T) {
	app := importGitHub(t, []map[string]any{openIssue(1, "first")}, nil)
	_, post := importServer(t, app)

	post(map[string]any{"repo": "acme/widgets", "state": "open"})
	rr := post(map[string]any{"repo": "acme/widgets", "state": "open"})
	var got struct {
		Issues struct{ New, Updated int } `json:"issues"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Issues.New != 0 || got.Issues.Updated != 1 {
		t.Fatalf("second run counts = %+v, want new=0 updated=1", got.Issues)
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	app := importGitHub(t, []map[string]any{openIssue(1, "first")}, nil)
	st, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "open", "dry_run": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got struct {
		Issues struct{ New, Updated int } `json:"issues"`
		DryRun bool                       `json:"dry_run"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if !got.DryRun || got.Issues.New != 1 {
		t.Fatalf("got %+v, want dry_run=true new=1", got)
	}
	issues, _ := st.ListIssues(context.Background(), "", "")
	if len(issues) != 0 {
		t.Fatalf("dry run stored %d issues, want 0", len(issues))
	}
	if n := countEvents(t, st); n != 0 {
		t.Fatalf("dry run wrote %d events, want 0", n)
	}
}

func TestImportRejectsUnmappedRepo(t *testing.T) {
	app := importGitHub(t, nil, nil)
	_, post := importServer(t, app)
	rr := post(map[string]any{"repo": "acme/unmapped", "state": "open"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — an unmapped repo's webhooks are ignored, so its import must be too", rr.Code)
	}
}

func TestImportWithoutAppReturns503(t *testing.T) {
	st := store.OpenTestStore(t)
	s := &server{st: st, cfg: Config{}, log: slog.Default(), appAuth: nil}
	req := httptest.NewRequest("POST", "/api/v1/inbox/import",
		bytes.NewReader([]byte(`{"repo":"acme/widgets"}`)))
	rr := httptest.NewRecorder()
	s.importInbox(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestImportDoesNotClobberPromotedRow(t *testing.T) {
	app := importGitHub(t, []map[string]any{openIssue(1, "renamed upstream")}, nil)
	st, post := importServer(t, app)
	ctx := context.Background()

	post(map[string]any{"repo": "acme/widgets", "state": "open"})

	if err := st.CreateActor(ctx, "someone", "human", "Someone", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}

	// Promote it, then re-import with a changed upstream title.
	var taskID string
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		task, err := store.PromoteIssue(tx, st.Now(), "acme/widgets", 1, store.TaskInput{
			ProjectID: "proj", Title: "kept", Priority: "low", Kind: "bug", CreatedBy: "someone",
		}, nil)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	post(map[string]any{"repo": "acme/widgets", "state": "open"})

	issues, err := st.ListIssues(ctx, "", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	got := issues[0]
	if got.TriageState != "promoted" {
		t.Errorf("triage_state = %q, want promoted — re-import must not reset triage", got.TriageState)
	}
	if got.TaskID == nil || *got.TaskID != taskID {
		t.Errorf("task_id = %v, want %s — re-import must not drop the task link", got.TaskID, taskID)
	}
	if got.Title != "renamed upstream" {
		t.Errorf("title = %q, want the refreshed upstream title", got.Title)
	}
}

func TestImportOfMergedPRLeavesTaskStateAlone(t *testing.T) {
	// The pulls fixture below must embed a real task id in head.ref for the
	// PR to correlate (see store.UpsertPR / store.TaskIDFromRef), and that id
	// has to exist before importGitHub bakes the fixture into its fake
	// server's response. So, unlike the other tests here, this one can't use
	// importGitHub/importServer in their usual order (app, then store, then
	// task) — it builds the store and task first, then the app, wiring the
	// server by hand exactly as importServer does.
	st0 := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st0.CreateProject(ctx, "proj", "Proj", "PR"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st0.AddRepo(ctx, "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if err := st0.CreateActor(ctx, "someone", "human", "Someone", false); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	var taskID string
	if err := st0.Tx(ctx, func(tx *sql.Tx) error {
		task, err := store.CreateTask(tx, st0.Now(), store.TaskInput{
			ProjectID: "proj", Title: "unrelated", Priority: "low", Kind: "bug", CreatedBy: "someone",
		})
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	pulls := []map[string]any{{
		"number": 1, "title": "old merged work", "state": "closed",
		"html_url": "https://gh/pr/1", "created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z", "merged_at": "2026-01-02T00:00:00Z",
		"merge_commit_sha": "deadbeef",
		"head":             map[string]any{"ref": store.BranchPrefix() + taskID + "-old", "sha": "cafe"},
	}}
	app := importGitHub(t, nil, pulls)
	s := &server{st: st0, cfg: Config{}, log: slog.Default(), appAuth: app}
	post := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/inbox/import", bytes.NewReader(b))
		rr := httptest.NewRecorder()
		s.importInbox(rr, req)
		return rr
	}

	rr := post(map[string]any{"repo": "acme/widgets", "state": "all", "include_prs": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	// Prove the correlation actually happened — otherwise the state
	// assertion below would pass vacuously even if import replayed the
	// lifecycle, because there would be no correlated task to replay it on.
	pr, err := st0.GetPR(ctx, "acme/widgets", 1)
	if err != nil {
		t.Fatalf("get pr: %v", err)
	}
	if pr.TaskID == nil || *pr.TaskID != taskID {
		t.Fatalf("pr task_id = %v, want %s — the fixture's head_ref must correlate to the task for this test to mean anything", pr.TaskID, taskID)
	}

	task, err := st0.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.State != "ready" {
		t.Fatalf("task state = %q, want ready — importing a merged PR must not replay the delivery lifecycle", task.State)
	}
}

// A GitHub list response can carry a zero merged_at ("0001-01-01T00:00:00Z")
// on a closed-unmerged PR (list.go leaves it un-normalized; ListPulls
// derives Merged from the same raw value). Import must not store that zero
// time as merged_at, matching the webhook path's guard.
func TestImportClosedUnmergedPRStoresNoMergedAt(t *testing.T) {
	pulls := []map[string]any{{
		"number": 1, "title": "closed without merge", "state": "closed",
		"html_url": "https://gh/pr/1", "created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z", "merged_at": "0001-01-01T00:00:00Z",
		"head": map[string]any{"ref": "unrelated-branch", "sha": "cafe"},
	}}
	app := importGitHub(t, nil, pulls)
	st, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "all", "include_prs": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	pr, err := st.GetPR(context.Background(), "acme/widgets", 1)
	if err != nil {
		t.Fatalf("get pr: %v", err)
	}
	if pr.State != "closed" {
		t.Fatalf("state = %q, want closed", pr.State)
	}
	if pr.MergedAt != nil {
		t.Fatalf("merged_at = %v, want nil for a closed-unmerged PR", pr.MergedAt)
	}
}
