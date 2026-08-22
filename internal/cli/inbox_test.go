package cli_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestClientInboxFlow(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, _, err := c.AddRepo(ctx, "proj", "acme/widgets", ""); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	// Seed two inbox issues the way a GitHub webhook delivery would: via
	// UpsertIssue wrapped in RecordEvent.
	seedIssue := func(number int64, title string) {
		t.Helper()
		_, _, err := st.RecordEvent(ctx, "github", "issue-open-"+title, "issues.opened", nil,
			func(tx *sql.Tx, _ int64) error {
				return store.UpsertIssue(tx, model.Issue{
					Repo: "acme/widgets", Number: number, Title: title, State: "open",
					URL: "https://github.com/acme/widgets/issues/1",
				}, time.Time{})
			})
		if err != nil {
			t.Fatalf("seed issue %q: %v", title, err)
		}
	}
	seedIssue(1, "Frobnicator is broken")
	seedIssue(2, "Not worth doing")

	list, _, err := c.ListIssues(ctx, "new", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) != 2 {
		t.Fatalf("ListIssues result = %+v", list.Issues)
	}

	task, _, err := c.PromoteIssue(ctx, model.PromoteInput{
		Repo: "acme/widgets", Number: 1, Priority: "high", Kind: "bug",
		AppliesToVersions: []string{"v1.2"},
	})
	if err != nil {
		t.Fatalf("PromoteIssue: %v", err)
	}
	if task.Title != "Frobnicator is broken" {
		t.Fatalf("promoted task title = %q, want issue title as default", task.Title)
	}

	if _, err := c.DismissIssue(ctx, "acme/widgets", 2); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}

	list, _, err = c.ListIssues(ctx, "new", "")
	if err != nil {
		t.Fatalf("ListIssues after triage: %v", err)
	}
	if len(list.Issues) != 0 {
		t.Fatalf("ListIssues after triage = %+v, want none left new", list.Issues)
	}
}

func TestImportInbox(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/inbox/import" {
			t.Errorf("path = %q, want /api/v1/inbox/import", r.URL.Path)
		}
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{
			"repo":   "acme/widgets",
			"issues": map[string]int{"new": 3, "updated": 1},
			"prs":    map[string]int{"new": 0, "updated": 0},
		})
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "t"})
	got, _, err := c.ImportInbox(context.Background(), model.ImportInput{
		Repo: "acme/widgets", State: "open", IncludePRs: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("ImportInbox: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if got.Issues.New != 3 || got.Issues.Updated != 1 {
		t.Fatalf("counts = %+v, want new=3 updated=1", got.Issues)
	}
	if gotBody["state"] != "open" || gotBody["include_prs"] != true || gotBody["dry_run"] != true {
		t.Fatalf("request body = %v, want state/include_prs/dry_run carried through", gotBody)
	}
	if _, ok := gotBody["since"]; ok {
		t.Fatalf("request body = %v, want no since key when Since is nil", gotBody)
	}

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, _, err := c.ImportInbox(context.Background(), model.ImportInput{
		Repo: "acme/widgets", Since: &since,
	}); err != nil {
		t.Fatalf("ImportInbox with Since: %v", err)
	}
	if gotBody["since"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("request body since = %v, want 2026-01-01T00:00:00Z", gotBody["since"])
	}
}
