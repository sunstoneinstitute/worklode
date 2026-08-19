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
	"time"

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

// fullIssuePageAt builds a full page (importPageSize entries) of issues for
// page (1-based), with updated_at increasing monotonically across the whole
// run: item i on page p gets base + ((p-1)*importPageSize + i) seconds. That
// makes the overall maximum predictable (the last item of the last page) and
// lets a fake server that always returns a full page force truncation without
// a huge literal fixture.
const importPageSize = 100

func fullIssuePageAt(page int, base time.Time) []map[string]any {
	items := make([]map[string]any, 0, importPageSize)
	for i := 0; i < importPageSize; i++ {
		n := (page-1)*importPageSize + i + 1
		ts := base.Add(time.Duration((page-1)*importPageSize+i) * time.Second)
		items = append(items, map[string]any{
			"number": n, "title": "t", "state": "open",
			"html_url": "u", "updated_at": ts.Format(time.RFC3339),
		})
	}
	return items
}

// importGitHubAlwaysFullIssues serves the installation handshake plus a full
// page of issues for every requested page, so importMaxPages is exhausted and
// the import truncates without needing a page-cap-sized literal fixture.
func importGitHubAlwaysFullIssues(t *testing.T, base time.Time) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets/issues":
			page := 1
			fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			json.NewEncoder(w).Encode(fullIssuePageAt(page, base))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// TestImportTruncatedReportsNewestUpdatedAt drives real truncation (every
// page full, importMaxPages exhausted) and checks the response names the
// exact timestamp a caller must pass to --since to resume: the newest
// updated_at among everything fetched this run.
func TestImportTruncatedReportsNewestUpdatedAt(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	app := importGitHubAlwaysFullIssues(t, base)
	_, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "open"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got struct {
		Truncated       bool       `json:"truncated"`
		NewestUpdatedAt *time.Time `json:"newest_updated_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("truncated = false, want true — the fixture serves importMaxPages full pages")
	}
	wantNewest := base.Add(time.Duration(importMaxPages*importPageSize-1) * time.Second)
	if got.NewestUpdatedAt == nil || !got.NewestUpdatedAt.Equal(wantNewest) {
		t.Fatalf("newest_updated_at = %v, want %v", got.NewestUpdatedAt, wantNewest)
	}
}

// TestImportUntruncatedOmitsNewestUpdatedAt guards the other direction: the
// field is meaningless (and must not be printed as a bogus --since) when the
// import already reached the end.
func TestImportUntruncatedOmitsNewestUpdatedAt(t *testing.T) {
	app := importGitHub(t, []map[string]any{openIssue(1, "first")}, nil)
	_, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "open"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("newest_updated_at")) {
		t.Fatalf("body = %s, want no newest_updated_at field for a non-truncated import", rr.Body)
	}
}

// fullPRPageAt mirrors fullIssuePageAt for pulls: a full page (importPageSize
// entries) with updated_at increasing monotonically, so a fake server that
// always returns a full page forces PR truncation the same way issues do.
func fullPRPageAt(page int, base time.Time) []map[string]any {
	items := make([]map[string]any, 0, importPageSize)
	for i := 0; i < importPageSize; i++ {
		n := (page-1)*importPageSize + i + 1
		ts := base.Add(time.Duration((page-1)*importPageSize+i) * time.Second)
		items = append(items, map[string]any{
			"number": n, "title": "t", "state": "open",
			"html_url": "u", "created_at": ts.Format(time.RFC3339),
			"updated_at": ts.Format(time.RFC3339),
			"head":       map[string]any{"ref": "unrelated-branch", "sha": "cafe"},
		})
	}
	return items
}

// importGitHubFullIssuesShortPulls serves a full page of issues for every
// requested page (so importMaxPages exhausts the issues stream, as in
// importGitHubAlwaysFullIssues) alongside a fixed, non-truncating page of
// pulls — letting a test drive the two streams' truncation independently.
func importGitHubFullIssuesShortPulls(t *testing.T, base time.Time, pulls []map[string]any) *githubauth.AppAuth {
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
			json.NewEncoder(w).Encode(fullIssuePageAt(page, base))
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

// importGitHubShortIssuesFullPulls is importGitHubFullIssuesShortPulls
// mirrored: a single short (non-truncating) page of issues alongside a full
// page of pulls for every requested page, so PRs truncate and issues do not.
func importGitHubShortIssuesFullPulls(t *testing.T, base time.Time) *githubauth.AppAuth {
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
			json.NewEncoder(w).Encode([]map[string]any{openIssue(1, "only")})
		case "/repos/acme/widgets/pulls":
			json.NewEncoder(w).Encode(fullPRPageAt(page, base))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &githubauth.AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// TestImportTruncatedIssuesNewestExcludesLaterPR reproduces Defect A: issues
// truncate at importMaxPages while a single PR carries an updated_at later
// than every issue. newest_updated_at must come from the issues stream only
// — the old code took the max across both streams, which pointed --since
// past unfetched issues (they're older than the PR) and silently dropped
// them on the next run.
func TestImportTruncatedIssuesNewestExcludesLaterPR(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantNewest := base.Add(time.Duration(importMaxPages*importPageSize-1) * time.Second)
	prUpdatedAt := wantNewest.Add(time.Hour)
	pulls := []map[string]any{{
		"number": 1, "title": "later pr", "state": "open",
		"html_url": "https://gh/pr/1", "created_at": prUpdatedAt.Format(time.RFC3339),
		"updated_at": prUpdatedAt.Format(time.RFC3339),
		"head":       map[string]any{"ref": "unrelated-branch", "sha": "cafe"},
	}}
	app := importGitHubFullIssuesShortPulls(t, base, pulls)
	_, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "open", "include_prs": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got struct {
		Issues          struct{ Truncated bool } `json:"issues"`
		PRs             struct{ Truncated bool } `json:"prs"`
		NewestUpdatedAt *time.Time               `json:"newest_updated_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Issues.Truncated {
		t.Fatalf("issues.truncated = false, want true — fixture serves importMaxPages full pages")
	}
	if got.PRs.Truncated {
		t.Fatalf("prs.truncated = true, want false — the pulls fixture is one short page")
	}
	if got.NewestUpdatedAt == nil || !got.NewestUpdatedAt.Equal(wantNewest) {
		t.Fatalf("newest_updated_at = %v, want %v (issues-only max, not the later PR timestamp %v)",
			got.NewestUpdatedAt, wantNewest, prUpdatedAt)
	}
}

// TestImportPRsTruncateIssuesDoNot is the reverse of the above: PRs hit the
// page cap while issues do not. It asserts issues.truncated and prs.truncated
// are reported independently in both directions, that the top-level
// truncated is their OR, and that newest_updated_at stays nil — a truncated
// PR list has no server-side since filter to resume with, so the cursor
// (which is issues-only) must not be set just because something truncated.
func TestImportPRsTruncateIssuesDoNot(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	app := importGitHubShortIssuesFullPulls(t, base)
	_, post := importServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets", "state": "open", "include_prs": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got struct {
		Truncated       bool                     `json:"truncated"`
		Issues          struct{ Truncated bool } `json:"issues"`
		PRs             struct{ Truncated bool } `json:"prs"`
		NewestUpdatedAt *time.Time               `json:"newest_updated_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Issues.Truncated {
		t.Fatalf("issues.truncated = true, want false — the issues fixture is one short page")
	}
	if !got.PRs.Truncated {
		t.Fatalf("prs.truncated = false, want true — fixture serves importMaxPages full pages of pulls")
	}
	if !got.Truncated {
		t.Fatalf("truncated = false, want true — top-level truncated is the OR of issues/prs")
	}
	if got.NewestUpdatedAt != nil {
		t.Fatalf("newest_updated_at = %v, want nil — cursor is issues-only and issues did not truncate", got.NewestUpdatedAt)
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
	extID, err := randomExternalID()
	if err != nil {
		t.Fatalf("random external id: %v", err)
	}
	var taskID string
	_, _, err = st.RecordEvent(ctx, "cli", extID, "issue.promoted", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.PromoteIssue(tx, st.Now(), "acme/widgets", 1, store.TaskInput{
				ProjectID: "proj", Title: "kept", Priority: "low", Kind: "bug", CreatedBy: "someone",
			}, nil, eventID)
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
	if got.TaskID != taskID {
		t.Errorf("task_id = %v, want %s — re-import must not drop the task link", got.TaskID, taskID)
	}
	if got.Title != "renamed upstream" {
		t.Errorf("title = %q, want the refreshed upstream title", got.Title)
	}
}

// countTaskCommits returns the number of task_commits rows attributed to
// taskID, via the store's cross-package test surface (DBForTests). Nothing in
// production reads this table back through the store's public API, so a
// delivery fact wrongly recorded by import would otherwise be invisible.
func countTaskCommits(t *testing.T, st *store.Store, taskID string) int {
	t.Helper()
	var n int
	if err := st.DBForTests().QueryRow(
		`SELECT count(*) FROM task_commits WHERE task_id = $1`, taskID).Scan(&n); err != nil {
		t.Fatalf("count task_commits: %v", err)
	}
	return n
}

// TestImportOfMergedPRLeavesTaskStateAlone fences the central rule of spec 020
// ("import is inventory, not replay"): importing a merged PR that correlates to
// a task must call none of Transition, CloseActiveLease, InsertTaskCommit, or
// ResolveDelivery. Each of those is observable here — the fixture puts the
// correlated task in the state where a replay would be visible: claimed (so an
// active lease exists to be wrongly closed, and the task sits in in_progress
// rather than a state a replay would leave untouched) and under a container (so a
// child transition would roll up and move the container).
//
// The container is attached after the claim, which is why it sits at ready rather
// than the in_progress its child implies: AddEdge does not roll up, only
// Transition does (see store.resolveParent), and the edges endpoint attaches
// existing tasks exactly this way. Starting the container at ready is also the
// sharper fence — from there any child transition at all, not just one to a
// closed state, changes the container's target state.
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
	var taskID, containerID string
	extID, err := randomExternalID()
	if err != nil {
		t.Fatalf("random external id: %v", err)
	}
	if _, _, err := st0.RecordEvent(ctx, "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			container, err := store.CreateTask(tx, st0.Now(), store.TaskInput{
				ProjectID: "proj", Title: "container", Priority: "low", Kind: "feature", CreatedBy: "someone",
			}, eventID)
			if err != nil {
				return err
			}
			containerID = container.ID
			task, err := store.CreateTask(tx, st0.Now(), store.TaskInput{
				ProjectID: "proj", Title: "unrelated", Priority: "low", Kind: "bug", CreatedBy: "someone",
			}, eventID)
			if err != nil {
				return err
			}
			taskID = task.ID
			return nil
		}); err != nil {
		t.Fatalf("create tasks: %v", err)
	}
	// Claim first, edge second: the claim's own ready -> in_progress is a real
	// lifecycle move (not the import path), and doing it before the edge exists
	// keeps it from rolling the container up.
	if _, err := st0.Claim(ctx, taskID, "someone", "some/worktree", 0); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	edgeExtID, err := randomExternalID()
	if err != nil {
		t.Fatalf("random external id: %v", err)
	}
	if _, _, err := st0.RecordEvent(ctx, "cli", edgeExtID, "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, st0.Now(), taskID, containerID, "child_of", eventID)
		}); err != nil {
		t.Fatalf("add child_of edge: %v", err)
	}

	pulls := []map[string]any{{
		"number": 1, "title": "old merged work", "state": "closed",
		"html_url": "https://gh/pr/1", "created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z", "merged_at": "2026-01-02T00:00:00Z",
		"merge_commit_sha": "deadbeef",
		"head":             map[string]any{"ref": taskID + "-old", "sha": "cafe"},
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
	if task.State != "in_progress" {
		t.Errorf("task state = %q, want in_progress (the claimed state) — importing a merged PR must not replay the delivery lifecycle", task.State)
	}

	// A delivery fact recorded from history: nothing reads task_commits back,
	// so only a direct count catches an InsertTaskCommit in the import path.
	if n := countTaskCommits(t, st0, taskID); n != 0 {
		t.Errorf("task_commits rows for %s = %d, want 0 — import records inventory, not delivery facts", taskID, n)
	}

	// The claim is still live: a merged PR from history must not release
	// someone's worktree.
	if _, err := st0.ActiveLease(ctx, taskID); err != nil {
		t.Errorf("active lease on %s: %v — import must not close a lease held by an active claim", taskID, err)
	}

	// Spec 004's roll-up runs off Transition, so a container that moved is proof a
	// child transition happened even where the child's own state looks benign.
	container, err := st0.GetTask(ctx, containerID)
	if err != nil {
		t.Fatalf("get container: %v", err)
	}
	if container.State != "ready" {
		t.Errorf("container state = %q, want ready — import must not drive the container roll-up", container.State)
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
