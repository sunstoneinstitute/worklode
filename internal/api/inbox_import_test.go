package api

import (
	"bytes"
	"context"
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
