---
status: superseded
covers: docs/specs/019-project-scoping.md
---
# Repo-scoped CLI commands — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every project-aware `lode` command default to the current
repo's project, resolved from config or the git remote, with `--project` /
`--repo` to override and `--project=` to opt out.

**Architecture:** The server learns to normalize a git remote URL to
`owner/name` and resolve it to a project (`GET /api/v1/projects/resolve`), and
to filter the inbox by project. The CLI grows a resolution chain
(flag → repo config → user config → git remote → unscoped) behind one
`cli.ResolveScope` function, backed by a TTL cache at
`~/.cache/worklode/remotes.json` so the remote lookup costs one request per
repo per week. Every failure in the git-remote step degrades silently to
unscoped.

**Tech Stack:** Go 1.x, cobra CLI, `net/http` mux (Go 1.22 routing patterns),
PostgreSQL via `database/sql`, standard-library testing.

**Spec:** `docs/specs/019-project-scoping.md`

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/repourl/repourl.go` | `Normalize(raw) (owner/name, error)` — pure git-remote-URL parsing, no deps |
| `internal/repourl/repourl_test.go` | table test over every URL form and rejection |
| `internal/cli/remotecache.go` | the `~/.cache/worklode/remotes.json` file: load, save, TTL |
| `internal/cli/remotecache_test.go` | hit, expiry, negative entries, corruption, unwritable dir |
| `internal/cli/gitremote.go` | `gitRemoteURL(dir)` — `git remote get-url origin`, "" on any failure |
| `internal/cli/gitremote_test.go` | real temp git repos |
| `internal/cli/scope.go` | `Scope`, `ResolveScope`, `ProjectKey` — the resolution chain |
| `internal/cli/scope_test.go` | each chain step wins over the ones below; each failure falls through |
| `internal/cmd/scope.go` | cobra glue: `addScopeFlags`, `resolveScope`, `resolveTaskID` |
| `internal/cmd/scope_test.go` | per-command flag wiring and bare-number expansion |

**Modified files**

| Path | Change |
|---|---|
| `internal/api/server.go:285-289` | register `GET /api/v1/projects/resolve` |
| `internal/api/admin.go` | `resolveProjectByRemote` handler; `listInbox` gains `project` |
| `internal/store/inbox.go:152` | `ListIssues` gains a project filter |
| `internal/cli/client.go` | `Config.CurrentProjectPath`; `ResolveRemote`; `ListIssues` gains project |
| `internal/cmd/root.go:75-83` | `resolveProject` replaced by `internal/cmd/scope.go` |
| `internal/cmd/task.go` | scope flags on `add`/`list`/`claim`; bare-number ids |
| `internal/cmd/board.go` | `--project`/`--repo` and default scope |
| `internal/cmd/inbox.go` | `--project`/`--repo` and default scope |
| `internal/cmd/lifecycle.go` | bare-number ids; `status` reports the scope |
| `internal/cmd/timeline.go` | bare-number ids |
| `internal/cmd/project.go` | `lode project resolve` |
| `README.md` | document the chain, the cache, and `project resolve` |

**Test commands**

- Package tests: `go test ./internal/repourl/... ./internal/cli/...`
- Store/API/cmd tests need Postgres (`store.OpenTestStore`): `go test ./internal/store/... ./internal/api/... ./internal/cmd/...`
- Everything: `go test ./...`

---

## Task 1: Normalize git remote URLs

**Files:**
- Create: `internal/repourl/repourl.go`
- Test: `internal/repourl/repourl_test.go`

- [ ] **Step 1: Write the failing test**

```go
package repourl_test

import (
	"errors"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/repourl"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"scp style", "git@github.com:sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"scp style no suffix", "git@github.com:sunstoneinstitute/worklode", "sunstoneinstitute/worklode"},
		{"scp style no user", "github.com:sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"https", "https://github.com/sunstoneinstitute/worklode", "sunstoneinstitute/worklode"},
		{"https with suffix", "https://github.com/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"git+ssh", "git+ssh://git@github.com/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"ssh with port", "ssh://git@github.com:22/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"git protocol", "git://github.com/sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
		{"bare owner/name", "sunstoneinstitute/worklode", "sunstoneinstitute/worklode"},
		{"trailing slash", "https://github.com/sunstoneinstitute/worklode/", "sunstoneinstitute/worklode"},
		{"surrounding space", "  git@github.com:sunstoneinstitute/worklode.git\n", "sunstoneinstitute/worklode"},
		{"other host", "git@git.example.com:sunstoneinstitute/worklode.git", "sunstoneinstitute/worklode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.Normalize(tc.in)
			if err != nil {
				t.Fatalf("Normalize(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"one segment", "worklode"},
		{"three segments", "https://github.com/a/b/c"},
		{"empty owner", "https://github.com//worklode"},
		{"empty name", "https://github.com/sunstoneinstitute/"},
		{"scheme only", "https://github.com"},
		{"not a url", "this is not a url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repourl.Normalize(tc.in)
			if err == nil {
				t.Fatalf("Normalize(%q) = %q; want an error", tc.in, got)
			}
			if !errors.Is(err, repourl.ErrNotRepoURL) {
				t.Fatalf("Normalize(%q) error = %v; want ErrNotRepoURL", tc.in, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/repourl/...`
Expected: FAIL — `no required module provides package .../internal/repourl`

- [ ] **Step 3: Write the implementation**

```go
// Package repourl normalizes the git remote URL forms `git remote get-url`
// emits down to the "owner/name" form worklode stores in project_repos.
//
// The host is deliberately discarded: project_repos.repo is unique on
// owner/name, so a mirror of a mapped repo on another host resolves to the
// same project.
package repourl

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotRepoURL is returned for input that does not name a repository.
var ErrNotRepoURL = errors.New("not a repository URL")

// Normalize turns a git remote URL into "owner/name".
//
// Accepted: scheme URLs (https://, ssh://, git://, git+ssh://, with or
// without a user, port, or .git suffix), scp-style host:path remotes
// (git@github.com:owner/name.git), and a bare owner/name.
func Normalize(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("empty remote: %w", ErrNotRepoURL)
	}

	if i := strings.Index(s, "://"); i >= 0 {
		// scheme://[user@]host[:port]/owner/name — drop everything through
		// the authority's trailing slash.
		rest := s[i+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", fmt.Errorf("remote %q has no path: %w", raw, ErrNotRepoURL)
		}
		s = rest[slash+1:]
	} else if c := strings.Index(s, ":"); c >= 0 {
		// scp-style [user@]host:owner/name — only when the colon comes
		// before any slash, so a path containing a colon is left alone.
		if slash := strings.Index(s, "/"); slash < 0 || c < slash {
			s = s[c+1:]
		}
	}

	s = strings.Trim(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("remote %q is not owner/name: %w", raw, ErrNotRepoURL)
	}
	return owner + "/" + name, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/repourl/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/repourl
git commit -m "Add repourl.Normalize for git remote URL forms"
```

---

## Task 2: Resolve a remote to a project over HTTP

**Files:**
- Modify: `internal/api/admin.go` (new handler, next to `listProjects`)
- Modify: `internal/api/server.go:285-289` (route registration)
- Test: `internal/api/projects_resolve_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// mapRepo creates a project and maps a repo to it.
func mapRepo(t *testing.T, h http.Handler, token, project, key, repo string) {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": project, "name": project, "key": key})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/projects/"+project+"/repos", token,
		map[string]string{"repo": repo})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("add repo: %d %s", rec.Code, rec.Body.String())
	}
}

func TestResolveRemoteFindsProject(t *testing.T) {
	_, h, token := newTestServer(t)
	mapRepo(t, h, token, "worklode", "WL", "sunstoneinstitute/worklode")

	for _, remote := range []string{
		"git@github.com:sunstoneinstitute/worklode.git",
		"https://github.com/sunstoneinstitute/worklode",
		"sunstoneinstitute/worklode",
	} {
		rec := doReq(t, h, http.MethodGet,
			"/api/v1/projects/resolve?remote="+urlQueryEscape(remote), token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("resolve %q: %d %s", remote, rec.Code, rec.Body.String())
		}
		var got struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "worklode" || got.Key != "WL" {
			t.Fatalf("resolve %q = %+v; want worklode/WL", remote, got)
		}
	}
	_ = context.Background()
}

func TestResolveRemoteUnmapped(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodGet,
		"/api/v1/projects/resolve?remote="+urlQueryEscape("git@github.com:acme/nope.git"), token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmapped repo: %d %s; want 404", rec.Code, rec.Body.String())
	}
}

func TestResolveRemoteInvalid(t *testing.T) {
	_, h, token := newTestServer(t)
	for _, remote := range []string{"", "worklode", "https://github.com/a/b/c"} {
		rec := doReq(t, h, http.MethodGet,
			"/api/v1/projects/resolve?remote="+urlQueryEscape(remote), token, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("remote %q: %d %s; want 422", remote, rec.Code, rec.Body.String())
		}
	}
}

func TestResolveRemoteRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	rec := doReq(t, h, http.MethodGet,
		"/api/v1/projects/resolve?remote=a%2Fb", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d; want 401", rec.Code)
	}
}
```

Add this helper to the same file (the test package has no query-escape helper
yet):

```go
// urlQueryEscape escapes a value for use in a query string.
func urlQueryEscape(s string) string { return url.QueryEscape(s) }
```

with `"net/url"` in the import block.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestResolveRemote -v`
Expected: FAIL — the route is unregistered, so requests 404 (and the
invalid-input cases return 404 rather than 422).

- [ ] **Step 3: Write the handler**

In `internal/api/admin.go`, directly after `listProjects` (ends at line 112),
add:

```go
// resolveProjectByRemote handles GET /api/v1/projects/resolve?remote=<url>:
// the repo → project mapping the CLI needs to scope commands to the repo it
// is run from. The URL is normalized here rather than in the CLI so a
// normalization fix ships without a client upgrade.
func (s *server) resolveProjectByRemote(w http.ResponseWriter, r *http.Request) {
	repo, err := repourl.Normalize(r.URL.Query().Get("remote"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	p, err := s.st.ProjectForRepo(r.Context(), repo)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	repos, err := s.st.ListRepos(r.Context(), p.ID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectJSON(p, repos))
}
```

Add `"github.com/sunstoneinstitute/worklode/internal/repourl"` to the import
block of `internal/api/admin.go`.

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, in the projects block at lines 285-289, add the
`resolve` route immediately after `GET /api/v1/projects`:

```go
	mux.Handle("GET /api/v1/projects", s.auth(s.listProjects))
	// Literal segment, so Go's mux prefers it over any future
	// GET /api/v1/projects/{id}. Read-only: no requireAdmin.
	mux.Handle("GET /api/v1/projects/resolve", s.auth(s.resolveProjectByRemote))
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run TestResolveRemote -v`
Expected: PASS (4 tests)

- [ ] **Step 6: Run the full API suite**

Run: `go test ./internal/api/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/api
git commit -m "Add GET /api/v1/projects/resolve for repo-to-project lookup"
```

---

## Task 3: Filter the inbox by project in the store

**Files:**
- Modify: `internal/store/inbox.go:150-178`
- Modify: `internal/api/admin.go` (the one `ListIssues` call site, ~line 372)
- Test: `internal/store/inbox_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/inbox_test.go`. That file is `package store`
(internal), so there is no `store.` qualifier, and it already has the helpers
`openInboxStore(t) *Store` (a store with project "horndb" and actor "stig")
and `upsertIssue(t, s, Issue) error` (which drives the package-level
`UpsertIssue` through `RecordEvent`).

```go
func TestListIssuesProjectFilter(t *testing.T) {
	s := openInboxStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "alpha", "Alpha", "AL"); err != nil {
		t.Fatalf("create project alpha: %v", err)
	}
	if err := s.CreateProject(ctx, "beta", "Beta", "BE"); err != nil {
		t.Fatalf("create project beta: %v", err)
	}
	if err := s.AddRepo(ctx, "alpha", "acme/alpha-app"); err != nil {
		t.Fatalf("map alpha repo: %v", err)
	}
	if err := s.AddRepo(ctx, "beta", "acme/beta-app"); err != nil {
		t.Fatalf("map beta repo: %v", err)
	}

	for _, is := range []Issue{
		{Repo: "acme/alpha-app", Number: 1, Title: "alpha", State: "open", URL: "https://example.test/1"},
		{Repo: "acme/beta-app", Number: 2, Title: "beta", State: "open", URL: "https://example.test/2"},
		{Repo: "acme/unmapped", Number: 3, Title: "unmapped", State: "open", URL: "https://example.test/3"},
	} {
		if err := upsertIssue(t, s, is); err != nil {
			t.Fatalf("upsert %s#%d: %v", is.Repo, is.Number, err)
		}
	}

	got := issueKeys(t, s, "", "alpha")
	if len(got) != 1 || got[0] != "acme/alpha-app#1" {
		t.Fatalf("project alpha = %v; want [acme/alpha-app#1]", got)
	}

	got = issueKeys(t, s, "", "")
	if len(got) != 3 {
		t.Fatalf("no project filter = %v; want all 3 issues", got)
	}

	got = issueKeys(t, s, "", "nosuchproject")
	if len(got) != 0 {
		t.Fatalf("unknown project = %v; want none", got)
	}
}

// issueKeys lists issues and returns "repo#number" for each.
func issueKeys(t *testing.T, s *Store, triageState, project string) []string {
	t.Helper()
	issues, err := s.ListIssues(t.Context(), triageState, project)
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	out := make([]string, 0, len(issues))
	for _, is := range issues {
		out = append(out, fmt.Sprintf("%s#%d", is.Repo, is.Number))
	}
	return out
}
```

Add `"fmt"` to that file's imports — it is not there yet.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestListIssues`
Expected: FAIL — compile error, `ListIssues` takes 2 args, not 3.

- [ ] **Step 3: Add the filter to the store**

Replace `ListIssues` in `internal/store/inbox.go` (currently lines 150-178):

```go
// ListIssues returns inbox issues, ordered by repo then number. An empty
// triageState or projectID disables that filter; a projectID with no mapped
// repos yields no issues. Issues carry a repo, and project_repos maps a repo
// to at most one project, so the project filter is a join.
func (s *Store) ListIssues(ctx context.Context, triageState, projectID string) ([]Issue, error) {
	q := `SELECT i.repo, i.number, i.title, i.state, i.triage_state, i.task_id,
	             i.applies_to_versions, i.url
	      FROM issues i`
	var args []any
	var where []string
	if projectID != "" {
		args = append(args, projectID)
		q += fmt.Sprintf(` JOIN project_repos pr ON pr.repo = i.repo AND pr.project_id = $%d`, len(args))
	}
	if triageState != "" {
		args = append(args, triageState)
		where = append(where, fmt.Sprintf(`i.triage_state = $%d`, len(args)))
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY i.repo, i.number`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()

	var out []Issue
	for rows.Next() {
		is, err := scanIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("scan issue: %w", err)
		}
		out = append(out, *is)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	return out, nil
}
```

Ensure `"strings"` is in the imports of `internal/store/inbox.go`.

- [ ] **Step 4: Update the call site**

In `internal/api/admin.go`, in `listInbox`, change:

```go
	issues, err := s.st.ListIssues(r.Context(), r.URL.Query().Get("state"))
```

to:

```go
	issues, err := s.st.ListIssues(r.Context(),
		r.URL.Query().Get("state"), r.URL.Query().Get("project"))
```

and update the handler's doc comment to
`// listInbox handles GET /api/v1/inbox?state=new&project=worklode.`

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run TestListIssues -v`
Expected: PASS

- [ ] **Step 6: Run the store and API suites**

Run: `go test ./internal/store/... ./internal/api/...`
Expected: PASS. If any other caller of `ListIssues` fails to compile, pass
`""` as the new third argument.

- [ ] **Step 7: Commit**

```bash
git add internal/store internal/api
git commit -m "Filter the inbox by project via the project_repos join"
```

---

## Task 4: Inbox project filter end to end (API test + client)

**Files:**
- Modify: `internal/cli/client.go:859` (`ListIssues`)
- Modify: `internal/cmd/inbox.go:26-46` (call site — flags come in Task 10)
- Test: `internal/api/inbox_test.go` (add)

- [ ] **Step 1: Write the failing API test**

Append to `internal/api/inbox_test.go` (create it if absent, `package api_test`):

```go
func TestListInboxProjectFilter(t *testing.T) {
	st, h, token := newTestServer(t)
	ctx := context.Background()
	mapRepo(t, h, token, "alpha", "AL", "acme/alpha-app")
	mapRepo(t, h, token, "beta", "BE", "acme/beta-app")
	seedIssue(t, st, "acme/alpha-app", 1)
	seedIssue(t, st, "acme/beta-app", 2)
	_ = ctx

	rec := doReq(t, h, http.MethodGet, "/api/v1/inbox?project=alpha", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list inbox: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []struct {
			Repo   string `json:"repo"`
			Number int64  `json:"number"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 || resp.Issues[0].Repo != "acme/alpha-app" {
		t.Fatalf("project=alpha returned %+v; want only acme/alpha-app#1", resp.Issues)
	}
}
```

`store.UpsertIssue` is a package-level function taking a `*sql.Tx`, not a
method, so seeding goes through the event log. Add this helper to the same
file:

```go
// seedIssue inserts a triage_state="new" inbox issue through the event log,
// the same path a GitHub webhook takes.
func seedIssue(t *testing.T, st *store.Store, repo string, number int64) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github",
		fmt.Sprintf("%s-%s-%d", t.Name(), repo, number), "issues.opened", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.UpsertIssue(tx, store.Issue{
				Repo: repo, Number: number, Title: "issue", State: "open",
				URL: "https://example.test/x",
			})
		})
	if err != nil {
		t.Fatalf("seed issue %s#%d: %v", repo, number, err)
	}
}
```

with `"database/sql"`, `"fmt"`, and `"context"` in the imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestListInboxProjectFilter -v`
Expected: PASS already if Task 3's handler change landed — in that case treat
this step as the regression proof and continue. If it FAILs, the query param
is not wired; fix `listInbox` before moving on.

- [ ] **Step 3: Add the project argument to the client**

In `internal/cli/client.go`, replace `ListIssues` (line 859):

```go
// ListIssues calls GET /api/v1/inbox. An empty state lists every triage
// state; an empty project lists every project's issues.
func (c *Client) ListIssues(ctx context.Context, state, project string) (IssueListResponse, []byte, error) {
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if project != "" {
		q.Set("project", project)
	}
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/inbox", q), nil)
	if err != nil {
		return IssueListResponse{}, nil, err
	}
	var resp IssueListResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return IssueListResponse{}, nil, fmt.Errorf("decode issue list: %w", err)
	}
	return resp, raw, nil
}
```

- [ ] **Step 4: Update the CLI call site**

In `internal/cmd/inbox.go`, change `c.ListIssues(cmd.Context(), state)` to
`c.ListIssues(cmd.Context(), state, "")`. The flags arrive in Task 10.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/api/... ./internal/cli/... ./internal/cmd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api internal/cli internal/cmd
git commit -m "Pass a project filter through the inbox client"
```

---

## Task 5: The remote cache file

**Files:**
- Create: `internal/cli/remotecache.go`
- Test: `internal/cli/remotecache_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()

	c := loadCache()
	c.putRemote("git@github.com:acme/app.git", "acme-app", now)
	c.putKey("acme-app", "AA", now)
	if err := c.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := loadCache()
	project, ok := got.remote("git@github.com:acme/app.git", now)
	if !ok || project != "acme-app" {
		t.Fatalf("remote = %q, %v; want acme-app, true", project, ok)
	}
	if key, ok := got.key("acme-app", now); !ok || key != "AA" {
		t.Fatalf("key = %q, %v; want AA, true", key, ok)
	}
}

func TestCacheHitExpires(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := time.Now()

	c := loadCache()
	c.putRemote("r", "p", base)
	if _, ok := c.remote("r", base.Add(cacheHitTTL-time.Minute)); !ok {
		t.Fatal("entry expired before its TTL")
	}
	if _, ok := c.remote("r", base.Add(cacheHitTTL+time.Minute)); ok {
		t.Fatal("entry survived past its TTL")
	}
}

func TestCacheMissExpiresSooner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := time.Now()

	c := loadCache()
	c.putRemote("r", "", base) // negative entry: repo not mapped
	project, ok := c.remote("r", base.Add(30*time.Minute))
	if !ok || project != "" {
		t.Fatalf("negative entry = %q, %v; want \"\", true", project, ok)
	}
	if _, ok := c.remote("r", base.Add(cacheMissTTL+time.Minute)); ok {
		t.Fatal("negative entry survived past the miss TTL")
	}
}

func TestCacheForget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()

	c := loadCache()
	c.putRemote("r", "p", now)
	c.forgetRemote("r")
	if _, ok := c.remote("r", now); ok {
		t.Fatal("forgotten entry still present")
	}
}

func TestCacheCorruptFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".cache", "worklode", "remotes.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := loadCache()
	if _, ok := c.remote("r", time.Now()); ok {
		t.Fatal("corrupt cache returned an entry")
	}
	// A corrupt file must not block a later write.
	c.putRemote("r", "p", time.Now())
	if err := c.save(); err != nil {
		t.Fatalf("save over corrupt file: %v", err)
	}
	if _, ok := loadCache().remote("r", time.Now()); !ok {
		t.Fatal("entry not persisted over a corrupt file")
	}
}

func TestCacheUnwritableDirIsNotFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A regular file where the cache directory should be: mkdir will fail.
	if err := os.MkdirAll(filepath.Join(home, ".cache"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cache", "worklode"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := loadCache()
	c.putRemote("r", "p", time.Now())
	if err := c.save(); err == nil {
		t.Fatal("save into an unwritable location returned nil error")
	}
	// Reading must still work (as an empty cache), not panic.
	if _, ok := loadCache().remote("r", time.Now()); ok {
		t.Fatal("unwritable cache returned an entry")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestCache`
Expected: FAIL — `undefined: loadCache`

- [ ] **Step 3: Write the implementation**

```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The remote cache saves a round-trip per command: resolving a git remote to
// a project needs the server, but repo→project mappings change rarely. A hit
// is trusted for a week; a miss only for an hour, so a repo mapped on the
// server just now starts working without any manual step.
const (
	cacheHitTTL  = 7 * 24 * time.Hour
	cacheMissTTL = time.Hour
)

// remoteEntry is a cached remote→project answer. An empty Project is a
// negative entry: the repo is not mapped to any project.
type remoteEntry struct {
	Project string    `json:"project"`
	At      time.Time `json:"at"`
}

// keyEntry is a cached project→task-id-key answer, for expanding bare task
// numbers.
type keyEntry struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// remoteCache is the on-disk cache at ~/.cache/worklode/remotes.json. It is
// pure optimization: every read failure yields an empty cache and every write
// failure is survivable, so no caller ever fails a command over it.
type remoteCache struct {
	Remotes map[string]remoteEntry `json:"remotes"`
	Keys    map[string]keyEntry    `json:"keys"`
}

// cachePath returns ~/.cache/worklode/remotes.json.
func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "worklode", "remotes.json"), nil
}

// loadCache reads the cache. A missing, unreadable, or corrupt file is an
// empty cache, never an error.
func loadCache() *remoteCache {
	c := &remoteCache{Remotes: map[string]remoteEntry{}, Keys: map[string]keyEntry{}}
	path, err := cachePath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var on remoteCache
	if err := json.Unmarshal(data, &on); err != nil {
		return c
	}
	if on.Remotes != nil {
		c.Remotes = on.Remotes
	}
	if on.Keys != nil {
		c.Keys = on.Keys
	}
	return c
}

// remote returns the cached project for a raw remote URL. The second result
// reports whether a fresh entry exists; a fresh entry with an empty project
// means "known to be unmapped".
func (c *remoteCache) remote(rawURL string, now time.Time) (string, bool) {
	e, ok := c.Remotes[rawURL]
	if !ok || !fresh(e.At, e.Project != "", now) {
		return "", false
	}
	return e.Project, true
}

// key returns the cached task-id key for a project id.
func (c *remoteCache) key(project string, now time.Time) (string, bool) {
	e, ok := c.Keys[project]
	if !ok || !fresh(e.At, e.Key != "", now) {
		return "", false
	}
	return e.Key, true
}

func (c *remoteCache) putRemote(rawURL, project string, now time.Time) {
	c.Remotes[rawURL] = remoteEntry{Project: project, At: now}
}

func (c *remoteCache) putKey(project, key string, now time.Time) {
	c.Keys[project] = keyEntry{Key: key, At: now}
}

// forgetRemote drops a cached remote answer, so the next resolution re-queries.
func (c *remoteCache) forgetRemote(rawURL string) { delete(c.Remotes, rawURL) }

// fresh reports whether an entry recorded at "at" is still valid: hits live a
// week, misses an hour.
func fresh(at time.Time, hit bool, now time.Time) bool {
	ttl := cacheMissTTL
	if hit {
		ttl = cacheHitTTL
	}
	return now.Sub(at) < ttl
}

// save writes the cache atomically (temp file + rename) with 0600
// permissions. Callers treat the error as advisory.
func (c *remoteCache) save() error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "remotes-*.json")
	if err != nil {
		return fmt.Errorf("create cache temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod cache temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace cache: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestCache -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/remotecache.go internal/cli/remotecache_test.go
git commit -m "Add the ~/.cache/worklode remote-to-project cache"
```

---

## Task 6: Read the repo's git remote

**Files:**
- Create: `internal/cli/gitremote.go`
- Test: `internal/cli/gitremote_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a git repo in a temp dir, optionally with an origin remote.
func initRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
	return dir
}

func TestGitRemoteURL(t *testing.T) {
	dir := initRepo(t, "git@github.com:acme/app.git")
	if got := gitRemoteURL(dir); got != "git@github.com:acme/app.git" {
		t.Fatalf("gitRemoteURL = %q; want git@github.com:acme/app.git", got)
	}
}

func TestGitRemoteURLFromSubdirectory(t *testing.T) {
	dir := initRepo(t, "https://github.com/acme/app")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := gitRemoteURL(sub); got != "https://github.com/acme/app" {
		t.Fatalf("gitRemoteURL from subdir = %q; want https://github.com/acme/app", got)
	}
}

func TestGitRemoteURLNoOrigin(t *testing.T) {
	if got := gitRemoteURL(initRepo(t, "")); got != "" {
		t.Fatalf("repo without origin = %q; want \"\"", got)
	}
}

func TestGitRemoteURLNotARepo(t *testing.T) {
	if got := gitRemoteURL(t.TempDir()); got != "" {
		t.Fatalf("non-repo directory = %q; want \"\"", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestGitRemote`
Expected: FAIL — `undefined: gitRemoteURL`

- [ ] **Step 3: Write the implementation**

```go
package cli

import (
	"os/exec"
	"strings"
)

// gitRemoteURL returns the origin remote URL of the repo containing dir, or
// "" when dir is not in a git repo, the repo has no origin, or git is not
// installed. Scope resolution treats "" as "no remote to resolve" and falls
// through to an unscoped command — a missing remote is never an error.
func gitRemoteURL(dir string) string {
	cmd := exec.Command("git", "-C", dir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestGitRemote -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/gitremote.go internal/cli/gitremote_test.go
git commit -m "Read the origin remote URL for scope resolution"
```

---

## Task 7: Record where current_project came from

**Files:**
- Modify: `internal/cli/client.go:39-44` (`Config`), `:110-160` (`loadConfigFrom`), `:212-223` (`merge`)
- Test: `internal/cli/client_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client_test.go`. That file is `package cli_test`
(external), so it reaches `loadConfigFrom` through the existing
`cli.LoadConfigFromForTest` export in `internal/cli/export_test.go`. Do not
set `LODE_SERVER`: leaving `ServerURL` empty keeps `loadConfigFrom` away from
the OS keychain.

```go
func TestCurrentProjectPathRecordsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "worklode")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user config: %v", err)
	}
	userPath := filepath.Join(userDir, "config.toml")
	if err := os.WriteFile(userPath, []byte("current_project = \"from-user\"\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	cfg, err := cli.LoadConfigFromForTest(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "from-user" || cfg.CurrentProjectPath != userPath {
		t.Fatalf("user config: project=%q path=%q; want from-user, %s",
			cfg.CurrentProject, cfg.CurrentProjectPath, userPath)
	}

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config: %v", err)
	}
	repoPath := filepath.Join(repo, ".worklode", "config.toml")
	if err := os.WriteFile(repoPath, []byte("current_project = \"from-repo\"\n"), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}

	cfg, err = cli.LoadConfigFromForTest(repo)
	if err != nil {
		t.Fatalf("load from repo: %v", err)
	}
	if cfg.CurrentProject != "from-repo" || cfg.CurrentProjectPath != repoPath {
		t.Fatalf("repo config: project=%q path=%q; want from-repo, %s",
			cfg.CurrentProject, cfg.CurrentProjectPath, repoPath)
	}
}
```

Ensure `"os"` and `"path/filepath"` are imported in that test file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestCurrentProjectPath`
Expected: FAIL — `cfg.CurrentProjectPath undefined`

- [ ] **Step 3: Add the field**

In `internal/cli/client.go`, extend `Config` (line 39):

```go
type Config struct {
	ServerURL      string
	Token          string
	CurrentProject string

	// CurrentProjectPath is the config file CurrentProject came from, so
	// commands can report which file set their scope. Empty when no file
	// set it.
	CurrentProjectPath string
}
```

- [ ] **Step 4: Set it when the user config supplies the project**

In `loadConfigFrom`, in the `case err == nil:` branch that parses the user
config, after the successful `parseConfig`, add:

```go
		if cfg.CurrentProject != "" {
			cfg.CurrentProjectPath = path
		}
```

- [ ] **Step 5: Set it when the repo config overrides**

Change `merge` to take the path, and update its one call site.

```go
// merge applies the non-empty values of a repo-local config (read from path)
// on top of cfg.
func (cfg *Config) merge(repo Config, path string) {
	if repo.ServerURL != "" && repo.ServerURL != cfg.ServerURL {
		// Same reasoning as the LODE_SERVER override in loadConfigFrom: a
		// legacy cleartext token in the user config belongs to that config's
		// server and must not leak onto a different one.
		cfg.Token = ""
		cfg.ServerURL = repo.ServerURL
	}
	if repo.CurrentProject != "" {
		cfg.CurrentProject = repo.CurrentProject
		cfg.CurrentProjectPath = path
	}
}
```

In `loadConfigFrom`, change `cfg.merge(repoCfg)` to `cfg.merge(repoCfg, repoPath)`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/cli/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "Record which config file set current_project"
```

---

## Task 8: The client's remote-resolve call

**Files:**
- Modify: `internal/cli/client.go` (next to `GetProject`, ~line 985)
- Test: `internal/cli/client_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client_test.go`:

```go
func TestResolveRemoteSendsRawURL(t *testing.T) {
	var gotPath, gotRemote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRemote = r.URL.Query().Get("remote")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{ServerURL: srv.URL, Token: "wl_test"})
	p, err := c.ResolveRemote(context.Background(), "git@github.com:sunstoneinstitute/worklode.git")
	if err != nil {
		t.Fatalf("ResolveRemote: %v", err)
	}
	if gotPath != "/api/v1/projects/resolve" {
		t.Fatalf("path = %q; want /api/v1/projects/resolve", gotPath)
	}
	if gotRemote != "git@github.com:sunstoneinstitute/worklode.git" {
		t.Fatalf("remote = %q; want the raw URL unmodified", gotRemote)
	}
	if p.ID != "worklode" || p.Key != "WL" {
		t.Fatalf("project = %+v; want worklode/WL", p)
	}
}

func TestResolveRemoteNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"not found"}`)
	}))
	defer srv.Close()

	c := NewClient(Config{ServerURL: srv.URL, Token: "wl_test"})
	if _, err := c.ResolveRemote(context.Background(), "git@github.com:acme/nope.git"); err == nil {
		t.Fatal("ResolveRemote on an unmapped repo returned nil error")
	}
}
```

Match the imports and the httptest/`NewClient` setup style the existing tests
in `internal/cli/client_test.go` already use; if they have a server helper,
use it instead of hand-rolling `httptest.NewServer`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestResolveRemote`
Expected: FAIL — `c.ResolveRemote undefined`

- [ ] **Step 3: Write the implementation**

Add to `internal/cli/client.go`, immediately after `GetProject`:

```go
// ResolveRemote calls GET /api/v1/projects/resolve, returning the project the
// given git remote URL maps to. The URL is sent exactly as git reported it —
// the server owns normalization — and a *ClientError with Status 404 means
// the repo is not mapped to any project.
func (c *Client) ResolveRemote(ctx context.Context, remote string) (Project, error) {
	q := url.Values{}
	q.Set("remote", remote)
	raw, err := c.do(ctx, http.MethodGet, withQuery("/api/v1/projects/resolve", q), nil)
	if err != nil {
		return Project{}, err
	}
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return Project{}, fmt.Errorf("decode project: %w", err)
	}
	return p, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestResolveRemote -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "Add Client.ResolveRemote"
```

---

## Task 9: The resolution chain

**Files:**
- Create: `internal/cli/scope.go`
- Test: `internal/cli/scope_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// resolveServer returns a client whose /api/v1/projects/resolve answers with
// the given status and body, and a counter of how many times it was called.
func resolveServer(t *testing.T, status int, body string) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{ServerURL: srv.URL, Token: "wl_test"}), &calls
}

const worklodeJSON = `{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]}`

func TestResolveScopeConfigBeatsRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:acme/app.git")
	c, calls := resolveServer(t, http.StatusOK, worklodeJSON)

	cfg := Config{CurrentProject: "from-config", CurrentProjectPath: "/repo/.worklode/config.toml"}
	got := ResolveScope(context.Background(), c, cfg, dir)
	if got.Project != "from-config" {
		t.Fatalf("project = %q; want from-config", got.Project)
	}
	if got.Source != ScopeRepoConfig {
		t.Fatalf("source = %q; want %q", got.Source, ScopeRepoConfig)
	}
	if *calls != 0 {
		t.Fatalf("server called %d times; a configured project must not query", *calls)
	}
}

func TestResolveScopeUserConfigSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c, _ := resolveServer(t, http.StatusOK, worklodeJSON)

	cfg := Config{CurrentProject: "p", CurrentProjectPath: home + "/.config/worklode/config.toml"}
	if got := ResolveScope(context.Background(), c, cfg, home); got.Source != ScopeUserConfig {
		t.Fatalf("source = %q; want %q", got.Source, ScopeUserConfig)
	}
}

func TestResolveScopeFromGitRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:sunstoneinstitute/worklode.git")
	c, calls := resolveServer(t, http.StatusOK, worklodeJSON)

	got := ResolveScope(context.Background(), c, Config{}, dir)
	if got.Project != "worklode" || got.Key != "WL" {
		t.Fatalf("scope = %+v; want worklode/WL", got)
	}
	if got.Source != ScopeGitRemote || got.Cached {
		t.Fatalf("scope = %+v; want an uncached git-remote source", got)
	}
	if *calls != 1 {
		t.Fatalf("server called %d times; want 1", *calls)
	}

	// Second resolution is served from the cache.
	got = ResolveScope(context.Background(), c, Config{}, dir)
	if got.Project != "worklode" || !got.Cached {
		t.Fatalf("second scope = %+v; want a cached worklode", got)
	}
	if *calls != 1 {
		t.Fatalf("server called %d times; the cache must prevent a second query", *calls)
	}
}

func TestResolveScopeUnmappedRepoIsCachedNegative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := initRepo(t, "git@github.com:acme/unmapped.git")
	c, calls := resolveServer(t, http.StatusNotFound, `{"error":"not found"}`)

	for i := 0; i < 2; i++ {
		got := ResolveScope(context.Background(), c, Config{}, dir)
		if got.Project != "" || got.Source != ScopeNone {
			t.Fatalf("unmapped repo scope = %+v; want an empty ScopeNone", got)
		}
	}
	if *calls != 1 {
		t.Fatalf("server called %d times; the negative entry must be cached", *calls)
	}
}

func TestResolveScopeDegradesSilently(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, `{"error":"boom"}`},
		{"malformed body", http.StatusOK, `not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			dir := initRepo(t, "git@github.com:acme/app.git")
			c, _ := resolveServer(t, tc.status, tc.body)
			if got := ResolveScope(context.Background(), c, Config{}, dir); got.Source != ScopeNone {
				t.Fatalf("scope = %+v; want ScopeNone", got)
			}
		})
	}

	t.Run("not a git repo", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		c, calls := resolveServer(t, http.StatusOK, worklodeJSON)
		if got := ResolveScope(context.Background(), c, Config{}, t.TempDir()); got.Source != ScopeNone {
			t.Fatalf("scope = %+v; want ScopeNone", got)
		}
		if *calls != 0 {
			t.Fatalf("server called %d times; no remote means no query", *calls)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		dir := initRepo(t, "git@github.com:acme/app.git")
		if got := ResolveScope(context.Background(), nil, Config{}, dir); got.Source != ScopeNone {
			t.Fatalf("scope = %+v; want ScopeNone", got)
		}
	})
}

func TestProjectKeyCachesLookup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"projects":[`+worklodeJSON+`]}`)
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, Token: "wl_test"})

	for i := 0; i < 2; i++ {
		if got := ProjectKey(context.Background(), c, "worklode"); got != "WL" {
			t.Fatalf("ProjectKey = %q; want WL", got)
		}
	}
	if calls != 1 {
		t.Fatalf("server called %d times; want 1 (cached thereafter)", calls)
	}
	if got := ProjectKey(context.Background(), c, ""); got != "" {
		t.Fatalf("ProjectKey(\"\") = %q; want \"\"", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestResolveScope|TestProjectKey'`
Expected: FAIL — `undefined: ResolveScope`

- [ ] **Step 3: Write the implementation**

```go
package cli

import (
	"context"
	"strings"
	"time"
)

// ScopeSource names the step of the resolution chain that produced a scope.
type ScopeSource string

const (
	ScopeFlag       ScopeSource = "flag"
	ScopeRepoConfig ScopeSource = "repo config"
	ScopeUserConfig ScopeSource = "user config"
	ScopeGitRemote  ScopeSource = "git remote"
	ScopeNone       ScopeSource = "none"
)

// Scope is a resolved project scope. An empty Project means "every project":
// nothing narrowed the command down, which is not an error.
type Scope struct {
	Project string
	Key     string // task-id key, e.g. "WL"; "" when not looked up
	Source  ScopeSource
	Path    string // config file, when Source is a config
	Remote  string // raw git remote URL, when Source is ScopeGitRemote
	Cached  bool   // the answer came from the local cache
}

// ResolveScope returns the project a command run in dir should act on, per
// docs/specs/019-project-scoping.md: repo config, then user config, then the
// git remote, then unscoped.
//
// It never fails. A missing remote, an unreachable server, an unmapped repo,
// or a malformed response all yield an unscoped result — scoping is a
// convenience, and losing it must not stop a command from running. A nil
// client skips the remote step.
func ResolveScope(ctx context.Context, c *Client, cfg Config, dir string) Scope {
	if cfg.CurrentProject != "" {
		return Scope{
			Project: cfg.CurrentProject,
			Source:  configSource(cfg.CurrentProjectPath),
			Path:    cfg.CurrentProjectPath,
		}
	}
	if c == nil || dir == "" {
		return Scope{Source: ScopeNone}
	}
	remote := gitRemoteURL(dir)
	if remote == "" {
		return Scope{Source: ScopeNone}
	}

	now := time.Now()
	cache := loadCache()
	if project, ok := cache.remote(remote, now); ok {
		if project == "" {
			return Scope{Source: ScopeNone, Remote: remote, Cached: true}
		}
		key, _ := cache.key(project, now)
		return Scope{
			Project: project, Key: key,
			Source: ScopeGitRemote, Remote: remote, Cached: true,
		}
	}

	p, err := c.ResolveRemote(ctx, remote)
	if err != nil {
		// Only a definite "no such mapping" is worth remembering. A transient
		// failure must not pin this repo to unscoped for the next hour.
		if ce, ok := err.(*ClientError); ok && ce.Status == 404 {
			cache.putRemote(remote, "", now)
			_ = cache.save()
		}
		return Scope{Source: ScopeNone, Remote: remote}
	}
	if p.ID == "" {
		return Scope{Source: ScopeNone, Remote: remote}
	}

	cache.putRemote(remote, p.ID, now)
	if p.Key != "" {
		cache.putKey(p.ID, p.Key, now)
	}
	_ = cache.save()

	return Scope{Project: p.ID, Key: p.Key, Source: ScopeGitRemote, Remote: remote}
}

// ForgetRemote drops the cached answer for the repo containing dir, so the
// next resolution re-queries the server. Backs `lode project resolve --refresh`.
func ForgetRemote(dir string) {
	remote := gitRemoteURL(dir)
	if remote == "" {
		return
	}
	cache := loadCache()
	cache.forgetRemote(remote)
	_ = cache.save()
}

// ProjectKey returns the task-id key for a project ("WL" for worklode),
// consulting the cache before the server. Returns "" when the project is
// empty or unknown — callers treat that as "cannot expand a bare task number".
func ProjectKey(ctx context.Context, c *Client, project string) string {
	if project == "" || c == nil {
		return ""
	}
	now := time.Now()
	cache := loadCache()
	if key, ok := cache.key(project, now); ok {
		return key
	}
	p, err := c.GetProject(ctx, project)
	if err != nil || p.Key == "" {
		return ""
	}
	cache.putKey(project, p.Key, now)
	_ = cache.save()
	return p.Key
}

// configSource classifies the file that set current_project. The user config
// lives under ~/.config/worklode; anything else is a repo-local config.
func configSource(path string) ScopeSource {
	if path == "" {
		return ScopeUserConfig
	}
	if strings.Contains(path, "/.config/worklode/") {
		return ScopeUserConfig
	}
	return ScopeRepoConfig
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run 'TestResolveScope|TestProjectKey' -v`
Expected: PASS

- [ ] **Step 5: Run the whole cli suite**

Run: `go test ./internal/cli/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cli/scope.go internal/cli/scope_test.go
git commit -m "Add the project scope resolution chain"
```

---

## Task 10: Cobra glue — scope flags and resolution

**Files:**
- Create: `internal/cmd/scope.go`
- Modify: `internal/cmd/root.go:75-83` (remove `resolveProject`)
- Modify: `internal/cmd/task.go` (`add`, `list`, `claim`), `internal/cmd/lifecycle.go` (`next`)
- Test: `internal/cmd/scope_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupGitRepo creates a fake $HOME containing a git repo with the given
// origin remote and no .worklode config, chdirs into it, and returns its path.
func setupGitRepo(t *testing.T, origin string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
	t.Chdir(repo)
	return repo
}

func TestTaskListScopesFromGitRemote(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	scoped := createTestTask(t, c, "in the scoped project")
	other := createOtherProjectTask(t, c)

	setupGitRepo(t, "git@github.com:acme/proj.git")

	got := taskListIDs(t)
	if len(got) != 1 || got[0] != scoped.ID {
		t.Fatalf("task list = %v; want only %s (scoped off the git remote, not %s)",
			got, scoped.ID, other.ID)
	}
}

func TestTaskListUnmappedRepoIsUnscoped(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	scoped := createTestTask(t, c, "a task")
	other := createOtherProjectTask(t, c)

	setupGitRepo(t, "git@github.com:acme/not-mapped.git")

	got := taskListIDs(t)
	if len(got) != 2 {
		t.Fatalf("task list = %v; want both %s and %s (unmapped repo means unscoped)",
			got, scoped.ID, other.ID)
	}
}

func TestTaskListRepoFlagSelectsProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	scoped := createTestTask(t, c, "a task")
	createOtherProjectTask(t, c)

	setupRepoConfig(t, "") // a repo config with no current_project

	got := taskListIDs(t, "--repo", "acme/proj")
	if len(got) != 1 || got[0] != scoped.ID {
		t.Fatalf("task list --repo = %v; want only %s", got, scoped.ID)
	}
}

func TestProjectAndRepoFlagsConflict(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupRepoConfig(t, "")

	out, err := runLode(t, "task", "list", "--project", "proj", "--repo", "acme/proj")
	if err == nil {
		t.Fatalf("--project with --repo succeeded; want an error\noutput: %s", out)
	}
}

func TestEmptyProjectFlagOptsOut(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	createTestTask(t, c, "a task")
	createOtherProjectTask(t, c)

	setupGitRepo(t, "git@github.com:acme/proj.git")

	if got := taskListIDs(t, "--project="); len(got) != 2 {
		t.Fatalf("task list --project= = %v; want both tasks", got)
	}
}

func TestTaskAddResolvesProjectFromGitRemote(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")

	setupGitRepo(t, "git@github.com:acme/proj.git")

	task := addTask(t, "--title", "From the git remote")
	if task.Project != "proj" {
		t.Fatalf("project = %q; want proj", task.Project)
	}
}

func TestTaskAddWithoutAnyProjectFails(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupGitRepo(t, "") // a git repo with no origin

	out, err := runLode(t, "task", "add", "--title", "Nowhere")
	if err == nil {
		t.Fatalf("task add with no resolvable project succeeded\noutput: %s", out)
	}
}
```

Two helpers this test needs. Add them to `internal/cmd/scope_test.go` too,
adapting to the actual signatures of `lifecycleTestServer`, `setupProject`,
and `createTestTask` in `internal/cmd/lifecycle_test.go`:

```go
// mapProjectRepo maps a GitHub repo to a project on the test server.
func mapProjectRepo(t *testing.T, c *cli.Client, project, repo string) {
	t.Helper()
	if _, err := c.AddRepo(context.Background(), project, repo, ""); err != nil {
		t.Fatalf("map %s to %s: %v", repo, project, err)
	}
}

// createOtherProjectTask creates a task in a second project, so scoping has
// something to exclude.
func createOtherProjectTask(t *testing.T, c *cli.Client) cli.Task {
	t.Helper()
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{
		ID: "other", Name: "Other", Key: "OT",
	}); err != nil {
		t.Fatalf("create other project: %v", err)
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "other", Title: "in another project", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create other-project task: %v", err)
	}
	return task
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run 'TestTaskListScopes|TestTaskListRepoFlag|TestProjectAndRepo' -v`
Expected: FAIL — `--repo` is an unknown flag, and `task list` does not consult
the git remote.

- [ ] **Step 3: Write the cobra glue**

Create `internal/cmd/scope.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// scopeFlags holds the values of the --project/--repo pair a command
// registers with addScopeFlags.
type scopeFlags struct {
	project string
	repo    string
}

// addScopeFlags registers the --project/--repo pair on cmd. projectHelp
// describes what the project narrows ("filter by project id", "project id").
func addScopeFlags(cmd *cobra.Command, f *scopeFlags, projectHelp string) {
	cmd.Flags().StringVar(&f.project, "project", "",
		projectHelp+" (default: the current repo's project — from current_project in config, else the git remote); pass --project= for all projects")
	cmd.Flags().StringVar(&f.repo, "repo", "",
		"name the project by one of its repos, as owner/name (alternative to --project)")
}

// resolveScope returns the project scope a command should act on: an explicit
// --project/--repo when passed, otherwise the config/git-remote chain in
// cli.ResolveScope. An explicitly empty --project= means "every project" and
// stops the chain.
func resolveScope(ctx context.Context, cmd *cobra.Command, c *cli.Client, cfg cli.Config, f *scopeFlags) (cli.Scope, error) {
	projectSet := cmd.Flags().Changed("project")
	repoSet := cmd.Flags().Changed("repo")

	if projectSet && repoSet {
		return cli.Scope{}, errors.New("--project and --repo name the same thing; pass only one")
	}
	if repoSet {
		p, err := c.ResolveRemote(ctx, f.repo)
		if err != nil {
			return cli.Scope{}, fmt.Errorf("resolve --repo %s: %w", f.repo, err)
		}
		return cli.Scope{Project: p.ID, Key: p.Key, Source: cli.ScopeFlag}, nil
	}
	if projectSet {
		return cli.Scope{Project: f.project, Source: cli.ScopeFlag}, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	return cli.ResolveScope(ctx, c, cfg, wd), nil
}

// bareTaskNumber matches a task number without its project key, as accepted
// by every id-taking command.
var bareTaskNumber = regexp.MustCompile(`^[0-9]+$`)

// resolveTaskID expands a bare task number ("12") to a full task id ("WL-12")
// using the current scope's project key. Anything else — including a full
// id from another project — is returned untouched.
func resolveTaskID(ctx context.Context, arg string, c *cli.Client, cfg cli.Config) (string, error) {
	if !bareTaskNumber.MatchString(arg) {
		return arg, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	scope := cli.ResolveScope(ctx, c, cfg, wd)
	key := scope.Key
	if key == "" {
		key = cli.ProjectKey(ctx, c, scope.Project)
	}
	if key == "" {
		return "", fmt.Errorf("%s is a task number, not a task id, and no current project is set:\npass a full id like WL-%s, or set current_project", arg, arg)
	}
	return key + "-" + arg, nil
}
```

- [ ] **Step 4: Remove the old helper**

In `internal/cmd/root.go`, delete `resolveProject` (lines 75-80) and
`projectFlagUsage` (line 83) — `addScopeFlags` owns the help text now.

- [ ] **Step 5: Rewire `task add`**

In `internal/cmd/task.go`, `newTaskAddCmd`: replace the `project` local with a
`scopeFlags`, and the resolution with `resolveScope`.

```go
func newTaskAddCmd() *cobra.Command {
	var scope scopeFlags
	var title, body, priority, kind, concern string
	var draft bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errors.New(`no project: pass --project or --repo, set current_project in .worklode/config.toml or ~/.config/worklode/config.toml, or map this repo with "lode project add-repo"`)
			}
			t, raw, err := c.CreateTask(cmd.Context(), cli.CreateTaskInput{
				Project: sc.Project, Title: title, Body: body, Priority: priority, Kind: kind,
				Concern: concern, Draft: draft,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []cli.Task{t})
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&priority, "priority", "medium", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kind, "kind", "feature", "kind: feature, bug, chore, spec")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security (optional)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as draft (not claimable until published with `lode task ready`)")
	cmd.MarkFlagRequired("title")
	return cmd
}
```

- [ ] **Step 6: Rewire `task list`**

```go
func newTaskListCmd() *cobra.Command {
	var scope scopeFlags
	var priority string
	var statuses []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (delivered and abandoned are hidden unless requested with --status)",
		RunE: func(cmd *cobra.Command, args []string) error {
			states := resolveStatusFilter(statuses)
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
				Project: sc.Project, States: states, Priority: priority,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), resp.Tasks)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "filter by project id")
	cmd.Flags().StringArrayVar(&statuses, "status", nil, "filter by status: draft, ready, in_progress, in_review, merged, deployed_dev, deployed_prod, released, abandoned, or all (repeatable; default hides merged, deployed_dev, deployed_prod, released, and abandoned)")
	cmd.Flags().StringVar(&priority, "priority", "", "filter by priority")
	return cmd
}
```

- [ ] **Step 7: Rewire `task claim --next` and `next`**

In `internal/cmd/task.go`'s claim command (line ~338) and
`internal/cmd/lifecycle.go`'s `runNext` (line ~130), replace the
`var project string` + `resolveProject(...)` pattern with `var scope scopeFlags`
+ `resolveScope(...)` exactly as in Step 6, feeding `sc.Project` where
`project` was used, and swap the `cmd.Flags().StringVar(&project, "project", ...)`
registration for `addScopeFlags(cmd, &scope, "restrict the pick to a project")`.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run 'TestTaskList|TestTaskAdd|TestProjectAndRepo|TestEmptyProject' -v`
Expected: PASS

- [ ] **Step 9: Run the whole cmd suite**

Run: `go test ./internal/cmd/...`
Expected: PASS. `resetProjectFlag` in `currentproject_test.go` still works —
the flag name is unchanged.

- [ ] **Step 10: Commit**

```bash
git add internal/cmd
git commit -m "Resolve command scope from config or the git remote"
```

---

## Task 11: Scope `board` and `inbox list`

**Files:**
- Modify: `internal/cmd/board.go`
- Modify: `internal/cmd/inbox.go:26-46`
- Test: `internal/cmd/scope_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/scope_test.go`:

```go
// boardProjectIDs runs `lode board --json` and returns the project ids shown.
func boardProjectIDs(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := runLode(t, append([]string{"board", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("lode board: %v\noutput: %s", err, out)
	}
	var resp struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode board %q: %v", out, err)
	}
	ids := make([]string, 0, len(resp.Projects))
	for _, p := range resp.Projects {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestBoardScopesToCurrentProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createOtherProjectTask(t, c)
	setupRepoConfig(t, "proj")

	if got := boardProjectIDs(t); len(got) != 1 || got[0] != "proj" {
		t.Fatalf("board = %v; want only proj", got)
	}
}

func TestBoardProjectFlagAndPositional(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createOtherProjectTask(t, c)
	setupRepoConfig(t, "proj")

	if got := boardProjectIDs(t, "--project", "other"); len(got) != 1 || got[0] != "other" {
		t.Fatalf("board --project other = %v; want only other", got)
	}
	if got := boardProjectIDs(t, "other"); len(got) != 1 || got[0] != "other" {
		t.Fatalf("board other = %v; want only other", got)
	}
	if got := boardProjectIDs(t, "--project="); len(got) != 2 {
		t.Fatalf("board --project= = %v; want both projects", got)
	}
}

func TestInboxListScopesToCurrentProject(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	createOtherProjectTask(t, c)
	mapProjectRepo(t, c, "other", "acme/other")
	seedIssue(t, st, "acme/proj", 1)
	seedIssue(t, st, "acme/other", 2)

	setupRepoConfig(t, "proj")

	out, err := runLode(t, "inbox", "list", "--json")
	if err != nil {
		t.Fatalf("lode inbox list: %v\noutput: %s", err, out)
	}
	var resp struct {
		Issues []struct {
			Repo string `json:"repo"`
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 || resp.Issues[0].Repo != "acme/proj" {
		t.Fatalf("inbox list = %+v; want only acme/proj", resp.Issues)
	}
}

// seedIssue inserts a triage_state="new" inbox issue through the event log,
// the same path a GitHub webhook takes.
func seedIssue(t *testing.T, st *store.Store, repo string, number int64) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github",
		fmt.Sprintf("%s-%s-%d", t.Name(), repo, number), "issues.opened", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.UpsertIssue(tx, store.Issue{
				Repo: repo, Number: number, Title: "issue", State: "open",
				URL: "https://example.test/x",
			})
		})
	if err != nil {
		t.Fatalf("seed issue %s#%d: %v", repo, number, err)
	}
}
```

`lifecycleTestServer(t)` returns `(*store.Store, *cli.Client)`, so `st` is its
first value. Add `"encoding/json"`, `"sort"`, `"context"`, `"database/sql"`,
`"fmt"` and the `store` import as needed.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run 'TestBoard|TestInboxList' -v`
Expected: FAIL — `board` has no `--project` flag and ignores `current_project`;
`inbox list` shows both issues.

- [ ] **Step 3: Rewrite `board`**

Replace `newBoardCmd` in `internal/cmd/board.go`:

```go
func newBoardCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "board [project]",
		Short: "Show the task board: what's in progress, in review, blocked, and ready",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			// The positional project is the older spelling of --project; it
			// wins over the resolution chain the same way the flag does.
			if len(args) == 1 {
				if cmd.Flags().Changed("project") || cmd.Flags().Changed("repo") {
					return errors.New("pass the project either positionally or with --project/--repo, not both")
				}
				scope.project = args[0]
				if err := cmd.Flags().Set("project", args[0]); err != nil {
					return err
				}
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Board(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.BoardRender(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "show one project's board")
	return cmd
}
```

Add `"errors"` to the imports of `internal/cmd/board.go`.

- [ ] **Step 4: Rewrite `inbox list`**

```go
func newInboxListCmd() *cobra.Command {
	var scope scopeFlags
	var state string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List inbox issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.ListIssues(cmd.Context(), state, sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.IssueTable(cmd.OutOrStdout(), resp.Issues)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "filter by project id")
	cmd.Flags().StringVar(&state, "state", "new", `triage state to list: "new", "promoted", "dismissed", or "" for all`)
	return cmd
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run 'TestBoard|TestInboxList' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cmd
git commit -m "Scope lode board and lode inbox list to the current project"
```

---

## Task 12: Bare task numbers

**Files:**
- Modify: `internal/cmd/task.go` (every id-taking subcommand)
- Modify: `internal/cmd/timeline.go`, `internal/cmd/lifecycle.go` (`next <id>`, `block --on`)
- Test: `internal/cmd/scope_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/scope_test.go`:

```go
func TestBareTaskNumberResolves(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "By number")
	setupRepoConfig(t, "proj")

	number := task.ID[strings.LastIndex(task.ID, "-")+1:]
	title, _ := taskTitleBody(t, number)
	if title != "By number" {
		t.Fatalf("task show %s = %q; want the task %s", number, title, task.ID)
	}
}

func TestFullTaskIDStillWorks(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "By full id")
	setupRepoConfig(t, "proj")

	if title, _ := taskTitleBody(t, task.ID); title != "By full id" {
		t.Fatalf("task show %s = %q; want By full id", task.ID, title)
	}
}

func TestBareTaskNumberWithoutProjectFails(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	createTestTask(t, c, "Unreachable by number")
	setupGitRepo(t, "") // no config, no remote

	out, err := runLode(t, "task", "show", "1")
	if err == nil {
		t.Fatalf("bare number with no project succeeded\noutput: %s", out)
	}
	if !strings.Contains(err.Error(), "task number") {
		t.Fatalf("error = %v; want it to explain that 1 is a task number", err)
	}
}
```

Ensure `"strings"` is imported.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestBareTaskNumber -v`
Expected: FAIL — `lode task show 1` requests task "1" and gets a 404.

- [ ] **Step 3: Convert every id-taking command**

Each of these currently starts its `RunE` with `c, err := newAPIClient()` and
passes `args[0]` straight through. Apply the same two-line change to all of
them:

```go
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
```

then replace every later use of `args[0]` in that `RunE` with `id`.

Commands to convert, with their current `newAPIClient()` line:

| File | Command | Line |
|---|---|---|
| `internal/cmd/task.go` | `task show` | ~150 |
| `internal/cmd/task.go` | `task edit` | ~206 |
| `internal/cmd/task.go` | `task ready` | ~255 |
| `internal/cmd/task.go` | `task reopen` | ~280 |
| `internal/cmd/task.go` | `task rework` | ~305 |
| `internal/cmd/task.go` | `task renew` | ~435 |
| `internal/cmd/task.go` | `task release` | ~461 |
| `internal/cmd/task.go` | `task done` | ~486 |
| `internal/cmd/task.go` | `task abandon` | ~511 |
| `internal/cmd/task.go` | `task block` | ~537 |
| `internal/cmd/task.go` | `task brief` | ~564 |
| `internal/cmd/task.go` | `task unblock` | ~611 |
| `internal/cmd/timeline.go` | `timeline` | — |

`task claim <id>` (task.go:338) already calls `newAPIClientWithConfig` after
Task 10; add the `resolveTaskID` call on its `args[0]` path only (the `--next`
path has no id).

`task block`/`task unblock` also take a `--by` task id: run it through
`resolveTaskID` as well, so `lode task block 12 --by 9` works.

In `internal/cmd/lifecycle.go`: `runNext` takes an optional `args[0]` task id —
resolve it the same way; and `block --on <id>` (line ~326) resolves its `--on`
value.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run 'TestBareTaskNumber|TestFullTaskID' -v`
Expected: PASS

- [ ] **Step 5: Run the whole cmd suite**

Run: `go test ./internal/cmd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cmd
git commit -m "Accept a bare task number where a task id is expected"
```

---

## Task 13: `lode project resolve` and `lode status`

**Files:**
- Modify: `internal/cmd/project.go` (new subcommand)
- Modify: `internal/cmd/lifecycle.go` (`status` output)
- Test: `internal/cmd/scope_test.go` (add)

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/scope_test.go`:

```go
func TestProjectResolveReportsSource(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	setupGitRepo(t, "git@github.com:acme/proj.git")

	out, err := runLode(t, "project", "resolve", "--json")
	if err != nil {
		t.Fatalf("lode project resolve: %v\noutput: %s", err, out)
	}
	var got struct {
		Project string `json:"project"`
		Key     string `json:"key"`
		Source  string `json:"source"`
		Remote  string `json:"remote"`
		Cached  bool   `json:"cached"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got.Project != "proj" || got.Key != "PROJ" {
		t.Fatalf("resolve = %+v; want project proj", got)
	}
	if got.Source != "git remote" || got.Remote != "git@github.com:acme/proj.git" {
		t.Fatalf("resolve = %+v; want the git-remote source", got)
	}
	if got.Cached {
		t.Fatalf("first resolve reported cached = true")
	}

	// Second run is cached; --refresh re-queries.
	out, err = runLode(t, "project", "resolve", "--json")
	if err != nil {
		t.Fatalf("second resolve: %v\noutput: %s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Cached {
		t.Fatalf("second resolve reported cached = false")
	}

	out, err = runLode(t, "project", "resolve", "--json", "--refresh")
	if err != nil {
		t.Fatalf("resolve --refresh: %v\noutput: %s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Cached {
		t.Fatalf("--refresh reported cached = true")
	}
}

func TestProjectResolveUnscoped(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	setupGitRepo(t, "")

	out, err := runLode(t, "project", "resolve")
	if err != nil {
		t.Fatalf("lode project resolve: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no current project") {
		t.Fatalf("output = %q; want it to say there is no current project", out)
	}
}
```

`setupProject` (`internal/cmd/lifecycle_test.go:152`) creates project `proj`
with key `PROJ`, which is what these assertions expect.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestProjectResolve -v`
Expected: FAIL — `unknown command "resolve" for "lode project"`

- [ ] **Step 3: Add the command**

In `internal/cmd/project.go`, add to the `project` command's `AddCommand`
list and define:

```go
// resolveResult is the --json form of `lode project resolve`.
type resolveResult struct {
	Project string `json:"project"`
	Key     string `json:"key,omitempty"`
	Source  string `json:"source"`
	Path    string `json:"path,omitempty"`
	Remote  string `json:"remote,omitempty"`
	Cached  bool   `json:"cached"`
}

func newProjectResolveCmd() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Show which project this directory scopes to, and why",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			if refresh {
				cli.ForgetRemote(wd)
			}
			sc := cli.ResolveScope(cmd.Context(), c, cfg, wd)
			if sc.Project != "" && sc.Key == "" {
				sc.Key = cli.ProjectKey(cmd.Context(), c, sc.Project)
			}

			if jsonOut(cmd) {
				b, err := json.Marshal(resolveResult{
					Project: sc.Project, Key: sc.Key, Source: string(sc.Source),
					Path: sc.Path, Remote: sc.Remote, Cached: sc.Cached,
				})
				if err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
				printRaw(cmd, b)
				return nil
			}

			o := cmd.OutOrStdout()
			if sc.Project == "" {
				fmt.Fprintln(o, "no current project: commands run across every project")
				fmt.Fprintln(o, `set current_project in .worklode/config.toml, or map this repo with "lode project add-repo"`)
				return nil
			}
			fmt.Fprintf(o, "%s%s — from %s\n", sc.Project, keySuffix(sc.Key), scopeOrigin(sc))
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-query the server instead of using the cached answer")
	return cmd
}

// keySuffix renders " (WL)" for a known task-id key, or nothing.
func keySuffix(key string) string {
	if key == "" {
		return ""
	}
	return " (" + key + ")"
}

// scopeOrigin describes where a scope came from, for humans.
func scopeOrigin(sc cli.Scope) string {
	switch sc.Source {
	case cli.ScopeRepoConfig, cli.ScopeUserConfig:
		return fmt.Sprintf("%s %s", sc.Source, sc.Path)
	case cli.ScopeGitRemote:
		cached := ""
		if sc.Cached {
			cached = " (cached)"
		}
		return fmt.Sprintf("git remote %s%s", sc.Remote, cached)
	default:
		return string(sc.Source)
	}
}
```

Ensure `"encoding/json"`, `"fmt"`, `"os"` and the `cli` package are imported
in `internal/cmd/project.go`, and that `newProjectResolveCmd()` is added to
the `cmd.AddCommand(...)` call in `newProjectCmd` (project.go:13-25).

- [ ] **Step 4: Report the scope in `lode status`**

In `internal/cmd/lifecycle.go`'s `newStatusCmd`, after obtaining the client,
resolve and report the scope. Change `newAPIClient()` to
`newAPIClientWithConfig()`, then add before the output block:

```go
			wd, err := os.Getwd()
			if err != nil {
				wd = ""
			}
			scope := cli.ResolveScope(cmd.Context(), c, cfg, wd)
```

Add `Project` and `ProjectSource` to `statusResult` and set them from
`scope.Project` / `string(scope.Source)`, and add one line to the human
output:

```go
			fmt.Fprintf(o, "project:  %s (%s)\n", orNone(scope.Project), scope.Source)
```

with:

```go
// orNone renders an empty scope as "-" rather than a blank column.
func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
```

Match the existing field alignment in that output block.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run 'TestProjectResolve|TestStatus' -v`
Expected: PASS

- [ ] **Step 6: Run everything**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cmd
git commit -m "Add lode project resolve, and report the scope in lode status"
```

---

## Task 14: Document the scoping model

**Files:**
- Modify: `README.md` (the "Setting the current project per repo" section)

- [ ] **Step 1: Rewrite the section**

Replace the section headed `### Setting the current project per repo` with:

````markdown
### Project scoping

Commands that act on a set of tasks — `lode task list`, `lode task add`,
`lode task claim --next`, `lode next`, `lode board`, `lode inbox list` — scope
themselves to the project of the repo you are in. The project is resolved in
this order, first hit wins:

1. `--project <id>` or `--repo <owner/name>` on the command line.
   `--project=` (explicitly empty) means *all projects*.
2. `current_project` in the repo's `.worklode/config.toml` (or `.lode/`),
   found by walking up from the working directory.
3. `current_project` in `~/.config/worklode/config.toml`.
4. The repo's `origin` git remote, resolved against the repo → project
   mappings created by `lode project add-repo`.
5. Nothing — the command runs across every project.

Step 4 needs no setup beyond the mapping already on the server, so a fresh
clone or a new worktree is scoped correctly on the first command. Its answer
is cached in `~/.cache/worklode/remotes.json` for a week (an unmapped repo for
an hour), so it costs one request per repo, not one per command. Anything that
goes wrong there — no remote, an unreachable server, an unmapped repo — falls
through to step 5 rather than failing the command.

To see what the current directory resolves to:

```bash
lode project resolve
# worklode (WL) — from git remote git@github.com:sunstoneinstitute/worklode.git (cached)

lode project resolve --refresh   # re-query the server
```

To pin a project explicitly, set it in `.worklode/config.toml` at the repo
root:

```toml
current_project = "sunstone-web"
```

```bash
lode task add --title "Fix the footer link"   # goes to sunstone-web
lode task list --project=                     # opt back out to all projects
lode board --repo sunstoneinstitute/other     # name a project by its repo
```

Inside a scoped repo, commands that take a task id also take a bare task
number: `lode task show 12` means `WL-12`. Full ids work everywhere.

The CLI merges the repo config over `~/.config/worklode/config.toml`. It may
set `server` and `current_project`, but not `token` — repo configs tend to be
committed, and the token belongs in the OS keychain (or `LODE_TOKEN`).
````

- [ ] **Step 2: Verify the documented commands actually behave that way**

Run each of these against a local server in a mapped repo and confirm the
output matches the README:

```bash
lode project resolve
lode project resolve --refresh
lode task list
lode board
lode inbox list
lode task show 1
```

- [ ] **Step 3: Run the full suite one more time**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "Document project scoping and the resolution chain"
```

---

## Self-Review Notes

Spec sections and the task implementing them:

| Spec section | Task |
|---|---|
| Remote normalization table | 1 |
| `GET /api/v1/projects/resolve` | 2 |
| `GET /api/v1/inbox?project=` | 3, 4 |
| Resolution chain steps 1-5 | 7, 9, 10 |
| Cache file, TTLs, atomic write, corruption tolerance | 5 |
| Silent degradation | 6, 9 |
| `lode project resolve --refresh` | 13 |
| Bare task numbers | 12 |
| `board`, `inbox list`, `task add` relaxation | 10, 11 |
| `status` reports the scope | 13 |
| README | 14 |

Deviations from the spec, deliberate:

- Invalid remotes return **422**, not 400 — every other validation failure in
  `internal/api` uses 422.
- The end-to-end "fresh clone scopes off its remote" check lives in
  `internal/cmd` (Task 10, `TestTaskListScopesFromGitRemote`) rather than
  `e2e/`. It uses a real `git init` repo and a real server, so it proves the
  same thing without a second harness.
