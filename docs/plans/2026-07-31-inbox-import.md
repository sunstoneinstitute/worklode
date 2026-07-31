# Inbox Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `docs/specs/020-inbox-import.md`: `lode inbox import` backfills a repo's existing GitHub issues and PRs through the same store functions the webhooks use, `lode inbox promote` gains `--draft` and `--parent` (and rejects `--kind epic`), `lode inbox link` attaches an issue to a task that already exists, and `lode project add-repo` warns when the App is not subscribed to every event the webhook handler routes.

**Architecture:** Import is **inventory, not replay**. `applyPullRequest` (`internal/hooks/github.go:253`) both upserts a PR row and drives the lifecycle (`Transition`, `CloseActiveLease`, `InsertTaskCommit`, `ResolveDelivery`); import calls only `store.UpsertIssue` / `store.UpsertPR` and never the lifecycle half, because those transitions encode *this just happened* and — since spec 018 — `Transition` ends in `resolveParent` (`internal/store/tasks.go:220`), so replaying history would invent epic state too. Fetching lives in `internal/githubauth`, returns plain structs, and imports no `store`; the API layer maps those structs into `store.Issue` / `store.PullRequest` exactly as the webhook handler maps payloads. One `RecordEvent` wraps the whole import, so it is one event and one transaction.

**Tech Stack:** Go 1.25+, Postgres (golang-migrate `*.up.sql`/`*.down.sql`), net/http `ServeMux`, cobra CLI. **No migration in this plan** — every table and constraint it needs already exists.

**Read first:** `docs/specs/020-inbox-import.md` (the spec), `internal/hooks/github.go:230-330` (`applyIssue`/`applyPullRequest` — the shapes import must match), `internal/store/inbox.go` (`UpsertIssue`, `PromoteIssue`, `DismissIssue`), `internal/store/changes.go:144-235` (`UpsertPR` and its correlation rules), `internal/api/admin.go:370-530` (the inbox handlers), `internal/githubauth/app.go` (`InstallationToken`, `DiscoverDoneState`, `repoPath`), `internal/api/appauth_test.go:40-100` (the fake-GitHub harness every API test here reuses).

**Conventions:**
- Run `go test ./internal/...` for the unit suite. **Store and API tests need a Postgres with pgvector** (migration `0007_skills` does `CREATE EXTENSION vector`). If the compose container on 5432 is a plain `postgres:17` image, either recreate it from `docker-compose.yml` (which pins `pgvector/pgvector:pg17`) or point the suite elsewhere: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/...`.
- Commit after every task, imperative mood, **no** `Co-authored-by:` or any other advertising trailer.
- Comments stay short and precise. Do not narrate the change history in a doc comment.
- Every new exported symbol gets a doc comment explaining *why*, matching the density of the surrounding file.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/githubauth/list.go` (new) | `ListIssues` / `ListPulls`: paged REST reads under an installation token, returning plain structs |
| `internal/githubauth/list_test.go` (new) | Paging, the `pull_request` filter, truncation |
| `internal/githubauth/app.go` | `SubscribedEvents` — the App's event subscriptions from `GET /app` |
| `internal/hooks/github.go` | `HandledEvents()` exported, and `applyFunc` switches over that same list |
| `internal/store/inbox.go` | `LinkIssue`, `ExistingIssueNumbers` |
| `internal/store/changes.go` | `ExistingPRNumbers` |
| `internal/api/inbox_import.go` (new) | `importInbox` handler — fetch, map, upsert in one event |
| `internal/api/admin.go` | `linkInbox`; `promoteRequest` gains `Draft`/`Parent`; epic guard; `addRepo` warnings |
| `internal/api/server.go` | Two routes |
| `internal/cli/client.go` | `ImportInbox`, `LinkIssue`, `PromoteInput` fields, typed `AddRepo` |
| `internal/cmd/inbox.go` | `lode inbox import`, `lode inbox link`, promote flags |
| `internal/cmd/project.go` | Print add-repo warnings |

Import handling lands in its own `internal/api/inbox_import.go` rather than growing `admin.go`, which is already ~660 lines covering actors, tokens, projects, repos, inbox, and the board.

---

## Task 1: GitHub list endpoints

**Files:**
- Create: `internal/githubauth/list.go`
- Test: `internal/githubauth/list_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/githubauth/list_test.go`:

```go
package githubauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// listServer serves /repos/acme/widgets/{issues,pulls} from fixed pages, plus
// the two calls InstallationToken makes. items[i] is page i+1.
func listServer(t *testing.T, kind string, pages [][]map[string]any) *AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			json.NewEncoder(w).Encode(map[string]any{"id": 7})
		case "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test"})
		case "/repos/acme/widgets/" + kind:
			page := 1
			fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			if page < 1 || page > len(pages) {
				json.NewEncoder(w).Encode([]map[string]any{})
				return
			}
			json.NewEncoder(w).Encode(pages[page-1])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &AppAuth{AppID: "12345", Key: appTestKey(), BaseURL: srv.URL}
}

// full builds a page of exactly maxPerPage entries so the pager keeps going.
func full(kind string, start int) []map[string]any {
	page := make([]map[string]any, 0, maxPerPage)
	for i := 0; i < maxPerPage; i++ {
		page = append(page, map[string]any{
			"number": start + i, "title": "t", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
		})
	}
	return page
}

func TestListIssuesSkipsPullRequests(t *testing.T) {
	app := listServer(t, "issues", [][]map[string]any{{
		{"number": 1, "title": "real issue", "state": "open",
			"html_url": "https://gh/1", "updated_at": "2026-01-01T00:00:00Z"},
		{"number": 2, "title": "a PR", "state": "open",
			"html_url": "https://gh/2", "updated_at": "2026-01-01T00:00:00Z",
			"pull_request": map[string]any{"url": "https://api/pulls/2"}},
	}})
	got, truncated, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 20)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("got %+v, want only issue 1 — entries with a pull_request key must be skipped", got)
	}
}

func TestListIssuesPagesUntilShortPage(t *testing.T) {
	app := listServer(t, "issues", [][]map[string]any{full("issues", 1), {
		{"number": 999, "title": "last", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z"},
	}})
	got, truncated, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 20)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(got) != maxPerPage+1 {
		t.Fatalf("len = %d, want %d", len(got), maxPerPage+1)
	}
}

func TestListIssuesTruncatesAtMaxPages(t *testing.T) {
	app := listServer(t, "issues", [][]map[string]any{full("issues", 1), full("issues", 101)})
	got, truncated, err := app.ListIssues(context.Background(), "acme/widgets", "open", time.Time{}, 2)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true — two full pages with maxPages=2 means more may remain")
	}
	if len(got) != 2*maxPerPage {
		t.Fatalf("len = %d, want %d", len(got), 2*maxPerPage)
	}
}

func TestListPullsDerivesMergedState(t *testing.T) {
	app := listServer(t, "pulls", [][]map[string]any{{
		{"number": 1, "title": "open one", "state": "open",
			"html_url": "u", "updated_at": "2026-01-01T00:00:00Z",
			"head": map[string]any{"ref": "lode/WL-1-x", "sha": "abc"}},
		{"number": 2, "title": "merged one", "state": "closed",
			"html_url": "u", "updated_at": "2026-01-02T00:00:00Z",
			"merged_at": "2026-01-02T00:00:00Z", "merge_commit_sha": "def",
			"head": map[string]any{"ref": "lode/WL-2-y", "sha": "bbb"}},
		{"number": 3, "title": "closed unmerged", "state": "closed",
			"html_url": "u", "updated_at": "2026-01-03T00:00:00Z",
			"head": map[string]any{"ref": "x", "sha": "ccc"}},
	}})
	got, _, err := app.ListPulls(context.Background(), "acme/widgets", "all", 20)
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// The list endpoint has no "merged" boolean; it must come from merged_at.
	if got[0].Merged || !got[1].Merged || got[2].Merged {
		t.Fatalf("merged flags = %v/%v/%v, want false/true/false",
			got[0].Merged, got[1].Merged, got[2].Merged)
	}
	if got[1].HeadRef != "lode/WL-2-y" || got[1].HeadSHA != "bbb" {
		t.Fatalf("head = %q/%q, want lode/WL-2-y/bbb", got[1].HeadRef, got[1].HeadSHA)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/githubauth/ -run 'TestList' -v`
Expected: FAIL — `undefined: maxPerPage`, `app.ListIssues undefined`, `app.ListPulls undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/githubauth/list.go`:

```go
// Paged REST reads of a repo's issues and pull requests, used by inbox import
// (spec 020), alongside the App authentication in app.go.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// maxPerPage is GitHub's maximum page size. The pager treats a short page as
// the end of the list, which is why the value must match what it requests.
const maxPerPage = 100

// Issue is one GitHub issue, carrying exactly the fields the inbox stores.
type Issue struct {
	Number    int64
	Title     string
	State     string
	HTMLURL   string
	UpdatedAt time.Time
}

// PullRequest is one GitHub pull request, carrying exactly the fields
// store.UpsertPR needs. Merged is derived: the list endpoint returns
// merged_at but no "merged" boolean, unlike the webhook payload.
type PullRequest struct {
	Number         int64
	Title          string
	State          string
	Merged         bool
	Body           string
	HTMLURL        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	MergedAt       *time.Time
	MergeCommitSHA *string
	HeadRef        string
	HeadSHA        string
}

// listQuery builds the shared per-page query string.
func listQuery(state string, page int) url.Values {
	q := url.Values{}
	q.Set("state", state)
	q.Set("per_page", strconv.Itoa(maxPerPage))
	q.Set("page", strconv.Itoa(page))
	return q
}

// ListIssues pages a repo's issues under an installation token. Entries
// carrying a pull_request key are skipped: GitHub's issues endpoint returns
// pull requests as issues, and without the filter every PR in the repo would
// land in the inbox as an issue. A zero since disables the filter. The bool
// reports truncation — maxPages exhausted without reaching a short page — so
// the caller can say so rather than silently importing a prefix.
func (a *AppAuth) ListIssues(ctx context.Context, repo, state string, since time.Time, maxPages int) ([]Issue, bool, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, false, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	auth := "Bearer " + token

	var out []Issue
	for page := 1; page <= maxPages; page++ {
		q := listQuery(state, page)
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}
		var raw []struct {
			Number      int64     `json:"number"`
			Title       string    `json:"title"`
			State       string    `json:"state"`
			HTMLURL     string    `json:"html_url"`
			UpdatedAt   time.Time `json:"updated_at"`
			PullRequest *struct{} `json:"pull_request"`
		}
		u := a.BaseURL + "/repos/" + path + "/issues?" + q.Encode()
		code, err := githubJSON(ctx, http.MethodGet, u, auth, &raw)
		if err != nil {
			return nil, false, err
		}
		if code != http.StatusOK {
			return nil, false, fmt.Errorf("list issues for %s: status %d", repo, code)
		}
		for _, it := range raw {
			if it.PullRequest != nil {
				continue
			}
			out = append(out, Issue{
				Number: it.Number, Title: it.Title, State: it.State,
				HTMLURL: it.HTMLURL, UpdatedAt: it.UpdatedAt,
			})
		}
		if len(raw) < maxPerPage {
			return out, false, nil
		}
	}
	return out, true, nil
}

// ListPulls pages a repo's pull requests under an installation token. The
// endpoint takes no since parameter, so callers filter on UpdatedAt.
func (a *AppAuth) ListPulls(ctx context.Context, repo, state string, maxPages int) ([]PullRequest, bool, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, false, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, false, err
	}
	auth := "Bearer " + token

	var out []PullRequest
	for page := 1; page <= maxPages; page++ {
		var raw []struct {
			Number         int64      `json:"number"`
			Title          string     `json:"title"`
			State          string     `json:"state"`
			Body           string     `json:"body"`
			HTMLURL        string     `json:"html_url"`
			CreatedAt      time.Time  `json:"created_at"`
			UpdatedAt      time.Time  `json:"updated_at"`
			MergedAt       *time.Time `json:"merged_at"`
			MergeCommitSHA *string    `json:"merge_commit_sha"`
			Head           struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
		}
		u := a.BaseURL + "/repos/" + path + "/pulls?" + listQuery(state, page).Encode()
		code, err := githubJSON(ctx, http.MethodGet, u, auth, &raw)
		if err != nil {
			return nil, false, err
		}
		if code != http.StatusOK {
			return nil, false, fmt.Errorf("list pulls for %s: status %d", repo, code)
		}
		for _, pr := range raw {
			merged := pr.MergedAt != nil && !pr.MergedAt.IsZero()
			out = append(out, PullRequest{
				Number: pr.Number, Title: pr.Title, State: pr.State, Merged: merged,
				Body: pr.Body, HTMLURL: pr.HTMLURL, CreatedAt: pr.CreatedAt,
				UpdatedAt: pr.UpdatedAt, MergedAt: pr.MergedAt,
				MergeCommitSHA: pr.MergeCommitSHA,
				HeadRef:        pr.Head.Ref, HeadSHA: pr.Head.SHA,
			})
		}
		if len(raw) < maxPerPage {
			return out, false, nil
		}
	}
	return out, true, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/githubauth/ -v`
Expected: PASS, including the pre-existing `app_test.go` cases.

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/list.go internal/githubauth/list_test.go
git commit -m "Add paged GitHub issue and pull-request list reads"
```

---

## Task 2: Store helpers for new-versus-updated counts

**Files:**
- Modify: `internal/store/inbox.go`, `internal/store/changes.go`
- Test: `internal/store/inbox_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/inbox_test.go`:

```go
func TestExistingIssueNumbers(t *testing.T) {
	st := OpenTestStore(t)
	seedProjectRepo(t, st, "proj", "acme/widgets")

	mustTx(t, st, func(tx *sql.Tx) error {
		if err := UpsertIssue(tx, Issue{Repo: "acme/widgets", Number: 1, Title: "a", State: "open"}); err != nil {
			return err
		}
		return UpsertIssue(tx, Issue{Repo: "acme/widgets", Number: 5, Title: "b", State: "open"})
	})

	mustTx(t, st, func(tx *sql.Tx) error {
		got, err := ExistingIssueNumbers(tx, "acme/widgets")
		if err != nil {
			return err
		}
		if len(got) != 2 || !got[1] || !got[5] {
			t.Fatalf("got %v, want {1,5}", got)
		}
		other, err := ExistingIssueNumbers(tx, "acme/other")
		if err != nil {
			return err
		}
		if len(other) != 0 {
			t.Fatalf("other repo got %v, want empty", other)
		}
		return nil
	})
}
```

If `seedProjectRepo` or `mustTx` do not already exist in the package's test helpers, use whatever the neighbouring tests in `inbox_test.go` use to open a store, create a project/repo mapping, and run a transaction — match them exactly rather than adding new helpers.

- [ ] **Step 2: Run test to verify it fails**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/store/ -run TestExistingIssueNumbers -v`
Expected: FAIL — `undefined: ExistingIssueNumbers`.

- [ ] **Step 3: Write the implementation**

Add to `internal/store/inbox.go`, after `UpsertIssue`:

```go
// ExistingIssueNumbers returns the inbox issue numbers already stored for
// repo. Import reads it before upserting so it can report new rows separately
// from updated ones — the upsert itself cannot distinguish the two, and a
// dry run must report the same split without writing.
func ExistingIssueNumbers(tx *sql.Tx, repo string) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT number FROM issues WHERE repo = $1`, repo)
	if err != nil {
		return nil, fmt.Errorf("existing issue numbers for %s: %w", repo, err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan issue number: %w", err)
		}
		out[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("existing issue numbers for %s: %w", repo, err)
	}
	return out, nil
}
```

Add the same shape to `internal/store/changes.go`, after `UpsertPR`:

```go
// ExistingPRNumbers returns the pull-request numbers already stored for repo.
// See ExistingIssueNumbers for why import needs it.
func ExistingPRNumbers(tx *sql.Tx, repo string) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT number FROM pull_requests WHERE repo = $1`, repo)
	if err != nil {
		return nil, fmt.Errorf("existing pr numbers for %s: %w", repo, err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan pr number: %w", err)
		}
		out[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("existing pr numbers for %s: %w", repo, err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/store/ -run TestExisting -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/inbox.go internal/store/changes.go internal/store/inbox_test.go
git commit -m "Report which imported rows are new"
```

---

## Task 3: The import endpoint

**Files:**
- Create: `internal/api/inbox_import.go`, `internal/api/inbox_import_test.go`
- Modify: `internal/api/server.go` (one route, beside the other inbox routes at `:404-406`)

- [ ] **Step 1: Write the failing test**

Create `internal/api/inbox_import_test.go`. Reuse `appTestKey` from `appauth_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run TestImport -v`
Expected: FAIL — `s.importInbox undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/api/inbox_import.go`:

```go
// Inbox import (spec 020): backfill a repo's existing GitHub issues and pull
// requests through the same store functions the webhook handler uses.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// importMaxPages caps each list at 20 pages of 100. Beyond it the response
// reports truncation and the caller narrows with --since, rather than the
// request running unbounded.
const importMaxPages = 20

// importTimeout bounds the GitHub round trips. They happen before the
// transaction opens, so a slow GitHub never holds a database lock.
const importTimeout = 60 * time.Second

var validImportStates = map[string]bool{"open": true, "closed": true, "all": true}

type importRequest struct {
	Repo       string     `json:"repo"`
	State      string     `json:"state"`
	IncludePRs bool       `json:"include_prs"`
	Since      *time.Time `json:"since"`
	DryRun     bool       `json:"dry_run"`
}

type importCounts struct {
	New     int `json:"new"`
	Updated int `json:"updated"`
}

type importResponse struct {
	Repo      string       `json:"repo"`
	Issues    importCounts `json:"issues"`
	PRs       importCounts `json:"prs"`
	Truncated bool         `json:"truncated"`
	DryRun    bool         `json:"dry_run"`
}

// importInbox handles POST /api/v1/inbox/import. It fetches outside any
// transaction, then applies every upsert inside one RecordEvent, so an import
// is one event and one transaction — and re-running it is safe, because
// UpsertIssue and UpsertPR never touch triage or correlation state that
// triage already set.
func (s *server) importInbox(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if s.appAuth == nil {
		writeErr(w, http.StatusServiceUnavailable, "github app not configured")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	if req.State == "" {
		req.State = "open"
	}
	if !validImportStates[req.State] {
		writeErr(w, http.StatusUnprocessableEntity, `invalid state: must be open, closed, or all`)
		return
	}
	if _, err := s.st.ProjectForRepo(r.Context(), req.Repo); err != nil {
		s.mapStoreErr(w, err)
		return
	}

	fctx, cancel := context.WithTimeout(r.Context(), importTimeout)
	defer cancel()

	var since time.Time
	if req.Since != nil {
		since = *req.Since
	}
	issues, truncated, err := s.appAuth.ListIssues(fctx, req.Repo, req.State, since, importMaxPages)
	if err != nil {
		s.log.Warn("import: list issues", "repo", req.Repo, "err", err)
		writeErr(w, http.StatusBadGateway, "github list issues failed")
		return
	}
	var pulls []githubauth.PullRequest
	if req.IncludePRs {
		prs, prTruncated, err := s.appAuth.ListPulls(fctx, req.Repo, req.State, importMaxPages)
		if err != nil {
			s.log.Warn("import: list pulls", "repo", req.Repo, "err", err)
			writeErr(w, http.StatusBadGateway, "github list pulls failed")
			return
		}
		truncated = truncated || prTruncated
		// The pulls endpoint has no since parameter, so filter here.
		for _, pr := range prs {
			if since.IsZero() || !pr.UpdatedAt.Before(since) {
				pulls = append(pulls, pr)
			}
		}
	}

	resp := importResponse{Repo: req.Repo, Truncated: truncated, DryRun: req.DryRun}

	count := func(tx *sql.Tx) error {
		haveIssues, err := store.ExistingIssueNumbers(tx, req.Repo)
		if err != nil {
			return err
		}
		havePRs, err := store.ExistingPRNumbers(tx, req.Repo)
		if err != nil {
			return err
		}
		for _, is := range issues {
			if haveIssues[is.Number] {
				resp.Issues.Updated++
			} else {
				resp.Issues.New++
			}
		}
		for _, pr := range pulls {
			if havePRs[pr.Number] {
				resp.PRs.Updated++
			} else {
				resp.PRs.New++
			}
		}
		return nil
	}

	if req.DryRun {
		// Counting needs a transaction but no event: a dry run must leave the
		// events table untouched too, not just the typed tables.
		if err := s.st.InTx(r.Context(), count); err != nil {
			s.mapStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "inbox.imported", payload,
		func(tx *sql.Tx, _ int64) error {
			if err := count(tx); err != nil {
				return err
			}
			for _, is := range issues {
				if err := store.UpsertIssue(tx, store.Issue{
					Repo:   req.Repo,
					Number: is.Number,
					Title:  is.Title,
					State:  is.State,
					URL:    is.HTMLURL,
				}); err != nil {
					return err
				}
			}
			for _, pr := range pulls {
				// Inventory only: no Transition, CloseActiveLease,
				// InsertTaskCommit, or ResolveDelivery. Those encode "this just
				// happened" and would rewrite lifecycle and epic state from
				// history. UpsertPR still correlates by head_ref/body.
				state := "open"
				if pr.State == "closed" {
					state = "closed"
					if pr.Merged {
						state = "merged"
					}
				}
				if _, err := store.UpsertPR(tx, store.PullRequest{
					Repo:     req.Repo,
					Number:   pr.Number,
					Title:    pr.Title,
					State:    state,
					HeadRef:  pr.HeadRef,
					HeadSHA:  pr.HeadSHA,
					MergeSHA: pr.MergeCommitSHA,
					URL:      pr.HTMLURL,
					OpenedAt: pr.CreatedAt,
					MergedAt: pr.MergedAt,
				}, pr.Body); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
```

`count` runs inside the same transaction as the upserts, and increments the response counters. Because `RecordEvent` retries nothing, the counters are written exactly once.

`store.Store` has no `InTx` helper yet — add one to `internal/store/store.go` in this task. Tasks 4 and 6 use it too:

```go
// InTx runs fn in a transaction, committing on success and rolling back on
// error. For read-only work that still needs transactional consistency —
// import's dry run, which must not write an event row.
func (s *Store) InTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, beside the other inbox routes:

```go
	mux.Handle("POST /api/v1/inbox/import", s.auth(requireAdmin(s.importInbox)))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run TestImport -v`
Expected: PASS (5 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/api/inbox_import.go internal/api/inbox_import_test.go internal/api/server.go internal/store/store.go
git commit -m "Add POST /api/v1/inbox/import"
```

---

## Task 4: Import does not disturb promoted rows or lifecycle state

**Files:**
- Test: `internal/api/inbox_import_test.go`

This task adds only tests. It is separate because these two properties are the spec's central claim, and they must fail loudly if a later change breaks them.

- [ ] **Step 1: Write the tests**

Append to `internal/api/inbox_import_test.go`:

```go
func TestImportDoesNotClobberPromotedRow(t *testing.T) {
	app := importGitHub(t, []map[string]any{openIssue(1, "renamed upstream")}, nil)
	st, post := importServer(t, app)
	ctx := context.Background()

	post(map[string]any{"repo": "acme/widgets", "state": "open"})

	// Promote it, then re-import with a changed upstream title.
	var taskID string
	err := st.InTx(ctx, func(tx *sql.Tx) error {
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
	pulls := []map[string]any{{
		"number": 1, "title": "old merged work", "state": "closed",
		"html_url": "https://gh/pr/1", "created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z", "merged_at": "2026-01-02T00:00:00Z",
		"merge_commit_sha": "deadbeef",
		"head":             map[string]any{"ref": "lode/WL-1-old", "sha": "cafe"},
	}}
	app := importGitHub(t, nil, pulls)
	st, post := importServer(t, app)
	ctx := context.Background()

	// A ready task the historical PR's branch name correlates to.
	var taskID string
	if err := st.InTx(ctx, func(tx *sql.Tx) error {
		task, err := store.CreateTask(tx, st.Now(), store.TaskInput{
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

	rr := post(map[string]any{"repo": "acme/widgets", "state": "all", "include_prs": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	task, err := st.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.State != "ready" {
		t.Fatalf("task state = %q, want ready — importing a merged PR must not replay the delivery lifecycle", task.State)
	}
}
```

`TestImportOfMergedPRLeavesTaskStateAlone` correlates only if the created task's id happens to be the one in `lode/WL-1-old`. Before running, check what id `CreateTask` allocates for project `proj` (the project key is `PR`, so the first id is `PR-1`); set the fixture's `head.ref` to `lode/<that id>-old` so correlation actually happens and the test proves the lifecycle is untouched rather than passing vacuously.

- [ ] **Step 2: Run the tests**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run 'TestImportDoesNotClobber|TestImportOfMergedPR' -v`
Expected: PASS. If either fails, the import path is doing more than upserting — fix `inbox_import.go`, not the test.

- [ ] **Step 3: Commit**

```bash
git add internal/api/inbox_import_test.go
git commit -m "Pin import to inventory-only semantics"
```

---

## Task 5: `lode inbox import`

**Files:**
- Modify: `internal/cli/client.go`, `internal/cmd/inbox.go`
- Test: `internal/cli/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client_test.go`, matching the surrounding tests' fake-server helper:

```go
func TestImportInbox(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/inbox/import" {
			t.Errorf("path = %q, want /api/v1/inbox/import", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{
			"repo":   "acme/widgets",
			"issues": map[string]int{"new": 3, "updated": 1},
			"prs":    map[string]int{"new": 0, "updated": 0},
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "t", http: srv.Client()}
	got, _, err := c.ImportInbox(context.Background(), ImportInput{
		Repo: "acme/widgets", State: "open", IncludePRs: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("ImportInbox: %v", err)
	}
	if got.Issues.New != 3 || got.Issues.Updated != 1 {
		t.Fatalf("counts = %+v, want new=3 updated=1", got.Issues)
	}
	if gotBody["state"] != "open" || gotBody["include_prs"] != true || gotBody["dry_run"] != true {
		t.Fatalf("request body = %v, want state/include_prs/dry_run carried through", gotBody)
	}
}
```

Match the `Client` construction to whatever the neighbouring tests in `client_test.go` use — field names may differ from the sketch above.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestImportInbox -v`
Expected: FAIL — `undefined: ImportInput`.

- [ ] **Step 3: Add the client method**

In `internal/cli/client.go`, in the inbox section (after `DismissIssue`):

```go
// ImportInput is the request body for ImportInbox. An empty State means the
// server default, "open".
type ImportInput struct {
	Repo       string     `json:"repo"`
	State      string     `json:"state,omitempty"`
	IncludePRs bool       `json:"include_prs,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
	DryRun     bool       `json:"dry_run,omitempty"`
}

// ImportCounts splits imported rows into ones that did not exist and ones
// that were refreshed.
type ImportCounts struct {
	New     int `json:"new"`
	Updated int `json:"updated"`
}

// ImportResult is the response from ImportInbox.
type ImportResult struct {
	Repo      string       `json:"repo"`
	Issues    ImportCounts `json:"issues"`
	PRs       ImportCounts `json:"prs"`
	Truncated bool         `json:"truncated"`
	DryRun    bool         `json:"dry_run"`
}

// ImportInbox calls POST /api/v1/inbox/import.
func (c *Client) ImportInbox(ctx context.Context, in ImportInput) (ImportResult, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/inbox/import", in)
	if err != nil {
		return ImportResult{}, nil, err
	}
	var out ImportResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return ImportResult{}, nil, fmt.Errorf("decode import response: %w", err)
	}
	return out, raw, nil
}
```

- [ ] **Step 4: Add the command**

In `internal/cmd/inbox.go`, register it and add the constructor:

```go
	cmd.AddCommand(newInboxListCmd(), newInboxPromoteCmd(), newInboxDismissCmd(),
		newInboxImportCmd(), newInboxLinkCmd())
```

```go
func newInboxImportCmd() *cobra.Command {
	var state, since string
	var includePRs, dryRun bool
	cmd := &cobra.Command{
		Use:   "import <repo>",
		Short: "Backfill an inbox from a repo's existing GitHub issues",
		Long: "Pages the GitHub REST API and upserts through the same path the webhooks use.\n" +
			"Re-running is safe and leaves already-triaged issues alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cli.ImportInput{
				Repo: args[0], State: state, IncludePRs: includePRs, DryRun: dryRun,
			}
			if since != "" {
				t, err := time.Parse(time.RFC3339, since)
				if err != nil {
					return fmt.Errorf("invalid --since %q: want RFC3339, e.g. 2026-01-01T00:00:00Z: %w", since, err)
				}
				in.Since = &t
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			res, raw, err := c.ImportInbox(cmd.Context(), in)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			prefix := ""
			if res.DryRun {
				prefix = "would import: "
			}
			fmt.Fprintf(out, "%s%s: %d new, %d updated issues; %d new, %d updated PRs\n",
				prefix, res.Repo, res.Issues.New, res.Issues.Updated, res.PRs.New, res.PRs.Updated)
			if res.Truncated {
				fmt.Fprintf(out, "warning: hit the page cap; re-run with --since to get the rest\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "open", `which to import: "open", "closed", or "all"`)
	cmd.Flags().BoolVar(&includePRs, "include-prs", false, "also import pull requests")
	cmd.Flags().StringVar(&since, "since", "", "only items updated at or after this RFC3339 time")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be imported without writing")
	return cmd
}
```

Add `"time"` to the file's imports.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli/ ./internal/cmd/ -v 2>&1 | tail -20`
Expected: PASS. `newInboxLinkCmd` does not exist yet, so this task will not compile until Task 7 — either add a stub returning `&cobra.Command{Use: "link"}` now and fill it in Task 7, or reorder so Task 7 lands first. Prefer the stub; keep the commit compiling.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go internal/cmd/inbox.go
git commit -m "Add lode inbox import"
```

---

## Task 6: `store.LinkIssue`

**Files:**
- Modify: `internal/store/inbox.go`
- Test: `internal/store/inbox_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/inbox_test.go`:

```go
func TestLinkIssueAttachesExistingTask(t *testing.T) {
	st := OpenTestStore(t)
	seedProjectRepo(t, st, "proj", "acme/widgets")

	var taskID string
	mustTx(t, st, func(tx *sql.Tx) error {
		if err := UpsertIssue(tx, Issue{Repo: "acme/widgets", Number: 1, Title: "a", State: "open"}); err != nil {
			return err
		}
		task, err := CreateTask(tx, st.Now(), TaskInput{
			ProjectID: "proj", Title: "already exists", Priority: "low", Kind: "bug", CreatedBy: "someone",
		})
		if err != nil {
			return err
		}
		taskID = task.ID
		return LinkIssue(tx, "acme/widgets", 1, taskID)
	})

	issues, err := st.ListIssues(context.Background(), "", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if issues[0].TriageState != "promoted" {
		t.Errorf("triage_state = %q, want promoted", issues[0].TriageState)
	}
	if issues[0].TaskID == nil || *issues[0].TaskID != taskID {
		t.Errorf("task_id = %v, want %s", issues[0].TaskID, taskID)
	}
}

func TestLinkIssueRejectsAlreadyTriaged(t *testing.T) {
	st := OpenTestStore(t)
	seedProjectRepo(t, st, "proj", "acme/widgets")

	mustTx(t, st, func(tx *sql.Tx) error {
		if err := UpsertIssue(tx, Issue{Repo: "acme/widgets", Number: 1, Title: "a", State: "open"}); err != nil {
			return err
		}
		return DismissIssue(tx, "acme/widgets", 1)
	})

	err := st.InTx(context.Background(), func(tx *sql.Tx) error {
		task, err := CreateTask(tx, st.Now(), TaskInput{
			ProjectID: "proj", Title: "t", Priority: "low", Kind: "bug", CreatedBy: "someone",
		})
		if err != nil {
			return err
		}
		return LinkIssue(tx, "acme/widgets", 1, task.ID)
	})
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("err = %v, want ErrBadTransition", err)
	}
}

func TestLinkIssueRejectsMissingTask(t *testing.T) {
	st := OpenTestStore(t)
	seedProjectRepo(t, st, "proj", "acme/widgets")

	mustTx(t, st, func(tx *sql.Tx) error {
		return UpsertIssue(tx, Issue{Repo: "acme/widgets", Number: 1, Title: "a", State: "open"})
	})

	err := st.InTx(context.Background(), func(tx *sql.Tx) error {
		return LinkIssue(tx, "acme/widgets", 1, "PR-999")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/store/ -run TestLinkIssue -v`
Expected: FAIL — `undefined: LinkIssue`.

- [ ] **Step 3: Write the implementation**

Add to `internal/store/inbox.go`, after `PromoteIssue`:

```go
// LinkIssue attaches an inbox issue to a task that already exists — the third
// triage outcome, for an issue whose work is already tracked. Like
// PromoteIssue it requires triage_state='new' and sets triage_state='promoted':
// "this issue has a task" is exactly what promoted means, so no new
// triage_state value (and no migration) is needed. The task must exist.
func LinkIssue(tx *sql.Tx, repo string, number int64, taskID string) error {
	var triageState string
	err := tx.QueryRow(
		`SELECT triage_state FROM issues WHERE repo = $1 AND number = $2`, repo, number,
	).Scan(&triageState)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("issue %s#%d: %w", repo, number, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get issue %s#%d triage_state: %w", repo, number, err)
	}
	if triageState != "new" {
		return fmt.Errorf("issue %s#%d is %s, not new: %w", repo, number, triageState, ErrBadTransition)
	}

	exists, err := taskExists(tx, taskID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}

	if _, err := tx.Exec(
		`UPDATE issues SET triage_state = 'promoted', task_id = $1 WHERE repo = $2 AND number = $3`,
		taskID, repo, number,
	); err != nil {
		return fmt.Errorf("link issue %s#%d to %s: %w", repo, number, taskID, err)
	}
	return nil
}
```

`taskExists` lives in `internal/store/changes.go:129` and is package-private, so it is directly callable.

- [ ] **Step 4: Run tests to verify they pass**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/store/ -run TestLinkIssue -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/store/inbox.go internal/store/inbox_test.go
git commit -m "Add LinkIssue: attach an inbox issue to an existing task"
```

---

## Task 7: `lode inbox link`

**Files:**
- Modify: `internal/api/admin.go`, `internal/api/server.go`, `internal/cli/client.go`, `internal/cmd/inbox.go`
- Test: `internal/api/inbox_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/inbox_test.go`, matching that file's existing server/token helpers:

```go
func TestLinkInbox(t *testing.T) {
	h, token, st := inboxServer(t) // match the helper the neighbouring tests use
	ctx := context.Background()

	seedIssue(t, st, "acme/widgets", 1, "an issue")
	taskID := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "already tracked", "priority": "low", "kind": "bug",
	})["id"].(string)

	rr := postJSON(t, h, token, "/api/v1/inbox/link", map[string]any{
		"repo": "acme/widgets", "number": 1, "task_id": taskID,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	issues, _ := st.ListIssues(ctx, "", "")
	if issues[0].TriageState != "promoted" || issues[0].TaskID == nil || *issues[0].TaskID != taskID {
		t.Fatalf("issue = %+v, want promoted and linked to %s", issues[0], taskID)
	}

	// Linking twice is a bad transition, not a silent overwrite.
	// mapStoreErr (internal/api/server.go:608) maps ErrBadTransition to 422.
	rr = postJSON(t, h, token, "/api/v1/inbox/link", map[string]any{
		"repo": "acme/widgets", "number": 1, "task_id": taskID,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second link status = %d, want 422", rr.Code)
	}
}
```

Before writing this, read `internal/api/inbox_test.go` and reuse its actual helpers; if it has none, copy the pattern from `internal/api/tasks_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run TestLinkInbox -v`
Expected: FAIL — 404, no such route.

- [ ] **Step 3: Add the handler**

In `internal/api/admin.go`, after `dismissInbox`:

```go
type linkRequest struct {
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
	TaskID string `json:"task_id"`
}

// linkInbox handles POST /api/v1/inbox/link: mark an inbox issue as covered
// by a task that already exists, instead of creating a new one.
func (s *server) linkInbox(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "repo is required")
		return
	}
	if strings.TrimSpace(req.TaskID) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "task_id is required")
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "issue.linked", payload,
		func(tx *sql.Tx, _ int64) error {
			return store.LinkIssue(tx, req.Repo, req.Number, req.TaskID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Route in `internal/api/server.go`:

```go
	mux.Handle("POST /api/v1/inbox/link", s.auth(s.linkInbox))
```

Plain `s.auth`, not `requireAdmin` — linking is triage, like promote and dismiss.

- [ ] **Step 4: Add the client method and command**

`internal/cli/client.go`:

```go
// LinkIssue calls POST /api/v1/inbox/link (204, no body): attach an inbox
// issue to a task that already exists.
func (c *Client) LinkIssue(ctx context.Context, repo string, number int64, taskID string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/inbox/link",
		map[string]any{"repo": repo, "number": number, "task_id": taskID})
}
```

`internal/cmd/inbox.go` — replace the Task 5 stub:

```go
func newInboxLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <repo> <number> <task-id>",
		Short: "Attach an inbox issue to a task that already exists",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := parseIssueNumber(args[1])
			if err != nil {
				return err
			}
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			taskID, err := resolveTaskID(cmd.Context(), args[2], c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.LinkIssue(cmd.Context(), args[0], number, taskID)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "linked %s#%d to %s\n", args[0], number, taskID)
			return nil
		},
	}
	return cmd
}
```

`resolveTaskID` (used by `lode task edit`, `internal/cmd/task.go:223`) expands a bare number under the current project, so `lode inbox link acme/widgets 41 7` works.

- [ ] **Step 5: Run tests to verify they pass**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ ./internal/cli/ ./internal/cmd/ 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin.go internal/api/server.go internal/api/inbox_test.go internal/cli/client.go internal/cmd/inbox.go
git commit -m "Add lode inbox link"
```

---

## Task 8: `promote --draft` and the epic guard

**Files:**
- Modify: `internal/api/admin.go`, `internal/cli/client.go`, `internal/cmd/inbox.go`
- Test: `internal/api/inbox_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/inbox_test.go`:

```go
func TestPromoteDraft(t *testing.T) {
	h, token, st := inboxServer(t)
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := postJSON(t, h, token, "/api/v1/inbox/promote", map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "draft": true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got map[string]any
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got["state"] != "draft" {
		t.Fatalf("state = %v, want draft — a bulk-promoted backlog must be stageable", got["state"])
	}
}

func TestPromoteRejectsEpicKind(t *testing.T) {
	h, token, st := inboxServer(t)
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := postJSON(t, h, token, "/api/v1/inbox/promote", map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "epic",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — a childless epic can never leave in_progress", rr.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run 'TestPromoteDraft|TestPromoteRejectsEpic' -v`
Expected: FAIL — state is `ready`, and `kind=epic` returns 201.

- [ ] **Step 3: Write the implementation**

In `internal/api/admin.go`, add to `promoteRequest`:

```go
	Draft bool `json:"draft"`
```

In `promoteInbox`, after the `validKinds` check:

```go
	// An epic's state follows its children (spec 018), and epicForbiddenStates
	// bars it from every delivery state — so an issue promoted as a childless
	// epic could never leave in_progress.
	if req.Kind == "epic" {
		writeErr(w, http.StatusUnprocessableEntity,
			"cannot promote an issue to kind epic: an epic's state follows its children; promote as a normal kind and use lode task decompose")
		return
	}
```

And pass the flag through to `store.TaskInput`:

```go
				Draft:     req.Draft,
```

In `internal/cli/client.go`, add to `PromoteInput`:

```go
	Draft bool `json:"draft,omitempty"`
```

In `internal/cmd/inbox.go`, in `newInboxPromoteCmd`, add `var draft bool` to the declarations, pass `Draft: draft` in the `cli.PromoteInput` literal, and register:

```go
	cmd.Flags().BoolVar(&draft, "draft", false, "create the task as a draft (not claimable until `lode task ready`)")
```

Also update the `--kind` usage string to drop `epic`, which is now rejected:

```go
	cmd.Flags().StringVar(&kind, "kind", "bug", "kind: feature, bug, chore, spec")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ ./internal/cmd/ -run 'Promote' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin.go internal/api/inbox_test.go internal/cli/client.go internal/cmd/inbox.go
git commit -m "Promote issues as drafts, and reject promoting to an epic"
```

---

## Task 9: `promote --parent`

**Files:**
- Modify: `internal/api/admin.go`, `internal/cli/client.go`, `internal/cmd/inbox.go`
- Test: `internal/api/inbox_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/inbox_test.go`:

```go
func TestPromoteUnderEpic(t *testing.T) {
	h, token, st := inboxServer(t)
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	epic := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "backlog", "priority": "low", "kind": "epic",
	})["id"].(string)

	rr := postJSON(t, h, token, "/api/v1/inbox/promote", map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "parent": epic,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got map[string]any
	json.Unmarshal(rr.Body.Bytes(), &got)

	parent, err := st.ParentOf(context.Background(), got["id"].(string))
	if err != nil {
		t.Fatalf("parent of promoted task: %v", err)
	}
	if parent == nil || parent.ID != epic {
		t.Fatalf("parent = %v, want %s", parent, epic)
	}
}

func TestPromoteUnknownParentIs404(t *testing.T) {
	h, token, st := inboxServer(t)
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := postJSON(t, h, token, "/api/v1/inbox/promote", map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "parent": "PR-999",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run TestPromoteU -v`
Expected: FAIL — no parent edge; unknown parent returns 201.

- [ ] **Step 3: Write the implementation**

In `internal/api/admin.go`, add to `promoteRequest`:

```go
	Parent string `json:"parent"`
```

In `promoteInbox`, after the epic guard, mirroring `createTask` (`internal/api/tasks.go:111-119`):

```go
	req.Parent = strings.TrimSpace(req.Parent)
	if req.Parent != "" {
		// Named 404 ahead of the transaction: AddEdge's own lookup stays the
		// authority for the rest of the spec-018 invariants, but its
		// ErrNotFound would otherwise be reported anonymously.
		if _, err := s.st.GetTask(r.Context(), req.Parent); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "parent not found: "+req.Parent)
			return
		}
	}
```

Inside the `RecordEvent` apply, after `PromoteIssue` succeeds and `created` is set:

```go
				if req.Parent != "" {
					// Same transaction as the promotion: there is no window
					// where the child exists unparented.
					if err := store.AddEdge(tx, s.st.Now(), t.ID, req.Parent, "child_of"); err != nil {
						return err
					}
				}
```

Add `"errors"` to the file's imports if it is not already there.

In `internal/cli/client.go`, add to `PromoteInput`:

```go
	Parent string `json:"parent,omitempty"`
```

In `internal/cmd/inbox.go`, add `parent` to the declared vars, pass `Parent: parent`, and register:

```go
	cmd.Flags().StringVar(&parent, "parent", "", "make the new task a child of this epic")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run TestPromote -v`
Expected: PASS (all four promote tests).

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin.go internal/api/inbox_test.go internal/cli/client.go internal/cmd/inbox.go
git commit -m "Promote issues under an epic"
```

---

## Task 10: Warn when the App is not subscribed to every handled event

**Files:**
- Modify: `internal/hooks/github.go`, `internal/githubauth/app.go`, `internal/api/admin.go`, `internal/cli/client.go`, `internal/cmd/project.go`
- Test: `internal/hooks/github_test.go`, `internal/api/appauth_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/hooks/github_test.go`:

```go
func TestHandledEventsMatchesApplyFunc(t *testing.T) {
	want := map[string]bool{
		"issues": true, "push": true, "pull_request": true, "deployment_status": true,
		"pull_request_review": true, "workflow_run": true, "release": true,
	}
	got := HandledEvents()
	if len(got) != len(want) {
		t.Fatalf("HandledEvents() = %v, want %d entries", got, len(want))
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected event %q", e)
		}
	}
}
```

Append to `internal/api/appauth_test.go` (extend `fakeGitHubApp.start` to serve `/app`, returning `f.events`):

```go
func TestAddRepoWarnsOnMissingEventSubscription(t *testing.T) {
	app := (&fakeGitHubApp{events: []string{"push", "pull_request"}}).start(t)
	_, post := addRepoServer(t, app)

	rr := post(map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — the check must never gate the mapping", rr.Code)
	}
	var got struct {
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Warnings) == 0 {
		t.Fatal("no warnings; want one naming the unsubscribed events")
	}
	if !strings.Contains(got.Warnings[0], "issues") {
		t.Errorf("warning = %q, want it to name the missing issues event", got.Warnings[0])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hooks/ -run TestHandledEvents -v` and `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/api/ -run TestAddRepoWarns -v`
Expected: FAIL — `undefined: HandledEvents`; no `warnings` field.

- [ ] **Step 3: Export the handled-event list and switch over it**

In `internal/hooks/github.go`, above `applyFunc`:

```go
// handledEvents are the GitHub event names applyFunc routes. It is the single
// source of truth: applyFunc switches over these names, and the add-repo
// subscription check compares an installation's subscriptions against them, so
// adding an eighth event cannot leave the check behind.
var handledEvents = []string{
	"issues", "push", "pull_request", "deployment_status",
	"pull_request_review", "workflow_run", "release",
}

// HandledEvents returns the event names this handler routes.
func HandledEvents() []string {
	out := make([]string, len(handledEvents))
	copy(out, handledEvents)
	return out
}
```

`applyFunc` keeps its `switch event` — a Go switch cannot range over a slice — so add a guard at the top that makes the drift impossible to miss:

```go
	if !slices.Contains(handledEvents, event) {
		return nil
	}
```

and delete the now-unreachable `default: return nil` arm only if the switch has no other fallthrough behaviour. Add `"slices"` to the imports.

- [ ] **Step 4: Add `SubscribedEvents`**

In `internal/githubauth/app.go`:

```go
// SubscribedEvents returns the event names this App is subscribed to, read
// from GET /app under the App JWT. The Apps settings page shows permissions
// and event subscriptions separately, so an App can hold issues:write and
// still never receive an issues event — this is what surfaces that.
func (a *AppAuth) SubscribedEvents(ctx context.Context) ([]string, error) {
	jwtStr, err := a.appJWT()
	if err != nil {
		return nil, err
	}
	var app struct {
		Events []string `json:"events"`
	}
	code, err := githubJSON(ctx, http.MethodGet, a.BaseURL+"/app", "Bearer "+jwtStr, &app)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("get app: status %d", code)
	}
	return app.Events, nil
}
```

- [ ] **Step 5: Wire the warning into addRepo**

In `internal/api/admin.go`, add a helper and call it from `addRepo` before the response:

```go
// subscriptionWarnings names the events the webhook handler routes that this
// installation is not subscribed to. Like done-state discovery it never gates
// the mapping: the repo is already mapped, and a GitHub failure must not fail
// the request — so any error yields no warnings.
func (s *server) subscriptionWarnings(ctx context.Context) []string {
	if s.appAuth == nil {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	subscribed, err := s.appAuth.SubscribedEvents(sctx)
	if err != nil {
		s.log.Warn("check app event subscriptions", "err", err)
		return nil
	}
	have := make(map[string]bool, len(subscribed))
	for _, e := range subscribed {
		have[e] = true
	}
	var missing []string
	for _, e := range hooks.HandledEvents() {
		if !have[e] {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"github app is not subscribed to: " + strings.Join(missing, ", ") +
		" — those webhooks will never arrive"}
}
```

Change `addRepo`'s response to carry them:

```go
	resp := map[string]any{"project_id": id, "repo": req.Repo, "done_state": doneState}
	if warnings := s.subscriptionWarnings(r.Context()); len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	writeJSON(w, http.StatusCreated, resp)
```

Add the `internal/hooks` import to `internal/api/admin.go`.

- [ ] **Step 6: Print the warnings**

In `internal/cli/client.go`, give `AddRepo` a typed return:

```go
// AddRepoResult is the response from AddRepo. Warnings are non-fatal setup
// problems — the mapping was created regardless.
type AddRepoResult struct {
	ProjectID string   `json:"project_id"`
	Repo      string   `json:"repo"`
	DoneState string   `json:"done_state"`
	Warnings  []string `json:"warnings,omitempty"`
}

// AddRepo calls POST /api/v1/projects/{id}/repos.
func (c *Client) AddRepo(ctx context.Context, projectID, repo, doneState string) (AddRepoResult, []byte, error) {
	body := map[string]string{"repo": repo}
	if doneState != "" {
		body["done_state"] = doneState
	}
	raw, err := c.do(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/repos", body)
	if err != nil {
		return AddRepoResult{}, nil, err
	}
	var out AddRepoResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return AddRepoResult{}, nil, fmt.Errorf("decode add-repo response: %w", err)
	}
	return out, raw, nil
}
```

In `internal/cmd/project.go`, `newProjectAddRepoCmd`:

```go
			res, raw, err := c.AddRepo(cmd.Context(), args[0], args[1], doneState)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "added %s to project %s\n", args[1], args[0])
			for _, warning := range res.Warnings {
				fmt.Fprintf(out, "warning: %s\n", warning)
			}
			return nil
```

Fix any other `AddRepo` callers the compiler names.

- [ ] **Step 7: Run the full suite**

Run: `TEST_POSTGRES_DSN="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" go test ./internal/... 2>&1 | tail -20`
Expected: every package `ok`.

- [ ] **Step 8: Commit**

```bash
git add internal/hooks/github.go internal/hooks/github_test.go internal/githubauth/app.go internal/api/admin.go internal/api/appauth_test.go internal/cli/client.go internal/cmd/project.go
git commit -m "Warn when the app is not subscribed to a handled event"
```

---

## Task 11: Documentation

**Files:**
- Modify: `README.md`, `docs/specs/020-inbox-import.md`

- [ ] **Step 1: Document the onboarding flow**

If `README.md` documents CLI commands, add `lode inbox import` to the onboarding sequence: map the repo, import, then triage. Show the shape that answers the spec's motivating case:

```bash
lode project add-repo myproject acme/widgets
lode inbox import acme/widgets --dry-run
lode inbox import acme/widgets
lode task add --project myproject --kind epic --title "acme/widgets backlog" --priority medium
lode inbox promote acme/widgets 41 --priority medium --draft --parent PR-12
```

- [ ] **Step 2: Flip the spec's status**

In `docs/specs/020-inbox-import.md`, change `**Status:** design` to `**Status:** spec` — matching how `018-task-hierarchy.md` was graduated on implementation.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/specs/020-inbox-import.md
git commit -m "Document inbox import and graduate spec 020"
```

---

## Self-review notes

**Spec coverage.** Fetch layer → Task 1. Import endpoint, preconditions, dry run, counts, truncation → Tasks 3–5. Inventory-not-replay → Task 4 (tests) enforcing Task 3's implementation. `--draft` and the epic guard → Task 8. `--parent` → Task 9. `lode inbox link` → Tasks 6–7. Event-subscription check → Task 10. Every row of the spec's Testing table maps to a named test above except "two pages then a short page" and "`maxPages` exceeded", which are Task 1's `TestListIssuesPagesUntilShortPage` and `TestListIssuesTruncatesAtMaxPages`.

**Verified against the worktree's base commit.** `store.Store.InTx` does not exist (Task 3 adds it). `mapStoreErr` maps `ErrBadTransition` to 422. `taskExists` is at `internal/store/changes.go:129` and is package-private, so `LinkIssue` can call it. Every `file:line` reference in this plan and in spec 020 was re-checked against this branch's base.

**The one thing to resolve during execution, not by guessing.** Test-helper names (`inboxServer`, `seedIssue`, `postJSON`, `mustTx`, `seedProjectRepo`, `createTaskViaAPI`) are written as the neighbouring tests' conventions rather than verified symbols; read each test file first and use what is actually there.

**Ordering constraint.** Task 5 registers `newInboxLinkCmd`, which Task 7 defines. Add the stub in Task 5 so every commit compiles.
