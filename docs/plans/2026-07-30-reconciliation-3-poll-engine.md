---
status: accepted
task: WL-12
covers: docs/specs/013-reconciliation.md
---
# Reconciliation 3/3: poll engine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of 3. Task numbering is global across the series: this plan
holds Tasks 10–13; `2026-07-30-reconciliation-1-replay-engine.md` (Tasks 1–4)
and `2026-07-30-reconciliation-2-cli-surface.md` (Tasks 5–9) must both be
merged first.

**Goal:** Engine 2: ask GitHub the current truth about candidate tasks and
repair what the webhook path missed, wired into the existing
`POST /api/v1/reconcile` handler so `lode reconcile` runs both engines.

**Architecture:** Engine 2 (`reconcile.Poll`) asks GitHub the current truth
about candidate tasks through per-repo installation-token clients
(`githubauth.RepoClient`), writes missing facts through the existing upserts
inside one `source='system'` event per run, then lets `store.ResolveDelivery`
advance states. Task 13 replaces the poll placeholder in the part-2 handler;
the response shape already carries the poll result, so the CLI renderer needs
no change.

**Tech Stack:** Go 1.x, `net/http` mux (Go 1.22 routing patterns),
PostgreSQL via `database/sql`, standard-library testing, `httptest` fakes for
the GitHub API.

**Spec:** `docs/specs/013-reconciliation.md`, read with its amendments from
`docs/specs/025-documents-in-the-backbone.md` §6: **engine 3
(spec-doc drift) and the `task_docs` table are superseded and must not be
built.** See part 1's header for the full series scope, prior-art map, and
what is owned elsewhere.

**Prerequisites (landed by parts 1–2):** the `0008` migration, engine 1
(`hooks.Replay`), `internal/store/reconcile.go` with the event-marker and
ingestion-health queries, the `POST /api/v1/reconcile` handler in
`internal/api/reconcile.go` with its poll placeholder, and the `lode
reconcile` command.

Design call this plan inherits (recorded in part 1): **`--task` does not
bound engine 1** — an ignored event's task binding is unknown before its
apply runs. When `task` is set, replay is skipped and only engine 2 runs.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/githubauth/repoclient.go` | `RepoClient`: per-repo installation-token client — PR, default branch, compare, releases |
| `internal/githubauth/repoclient_test.go` | every method against an `httptest` GitHub |
| `internal/reconcile/poll.go` | engine 2: gather GitHub truth, apply through one `reconcile.poll` system event |
| `internal/reconcile/poll_test.go` | merged-while-down repair; convergence; dry-run; attribution |

**Modified files**

| Path | Change |
|---|---|
| `internal/store/reconcile.go` | append `PollCandidates`, `UnlandedTaskCommits` (Task 11) |
| `internal/store/reconcile_test.go` | append their tests |
| `internal/api/reconcile.go` | replace the poll placeholder with `reconcile.Poll` (Task 13) |
| `internal/api/reconcile_test.go` | append poll-skipped test |
| `README.md` | document `doctor`, `project doctor`, `reconcile` |

**Test commands**

- Store/reconcile/API suites need Postgres (`store.OpenTestStore`):
  `go test ./internal/store/... ./internal/reconcile/... ./internal/api/...`
- No Postgres needed: `go test ./internal/githubauth/...`
- Everything: `go test ./...`

---
## Task 10: githubauth.RepoClient — the poll engine's GitHub reads

**Files:**
- Create: `internal/githubauth/repoclient.go`
- Test: `internal/githubauth/repoclient_test.go`

- [ ] **Step 1: Write the failing test**

`internal/githubauth/repoclient_test.go` (`package githubauth_test`; model
the fake — installation lookup + token mint routes, RSA key via
`rsa.GenerateKey` — on the existing `internal/githubauth/app_test.go`):

```go
package githubauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
)

// newFakeGitHub serves the app-auth routes plus per-path canned JSON bodies.
func newFakeGitHub(t *testing.T, routes map[string]string) *githubauth.AppAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/app/installation":
			io.WriteString(w, `{"id": 42}`)
		case "/app/installations/42/access_tokens":
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

func TestRepoClientPR(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "closed", "merged": true,
			"body": "", "html_url": "u",
			"merge_commit_sha": "2222222222222222222222222222222222222222",
			"merged_at": "2026-07-20T10:00:00Z",
			"created_at": "2026-07-19T09:00:00Z",
			"head": {"ref": "lode/WL-1-fix", "sha": "1111111111111111111111111111111111111111"}
		}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	pr, err := rc.PR(t.Context(), 12)
	if err != nil {
		t.Fatalf("PR: %v", err)
	}
	if !pr.Merged || pr.MergeCommitSHA == nil || *pr.MergeCommitSHA != "2222222222222222222222222222222222222222" {
		t.Fatalf("PR = %+v; want merged with a merge sha", pr)
	}
	if pr.HeadRef != "lode/WL-1-fix" || pr.MergedAt == nil {
		t.Fatalf("PR = %+v; want head ref and merged_at", pr)
	}
}

func TestRepoClientDefaultBranch(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	branch, err := rc.DefaultBranch(t.Context())
	if err != nil || branch != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", branch, err)
	}
}

func TestRepoClientCommitOnBranch(t *testing.T) {
	sha, off := "2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333"
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/compare/" + sha + "...main": `{"status": "ahead"}`,
		"/repos/acme/app/compare/" + off + "...main": `{"status": "diverged"}`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	on, err := rc.CommitOnBranch(t.Context(), "main", sha)
	if err != nil || !on {
		t.Fatalf("CommitOnBranch(ancestor) = %v, %v; want true", on, err)
	}
	on, err = rc.CommitOnBranch(t.Context(), "main", off)
	if err != nil || on {
		t.Fatalf("CommitOnBranch(diverged) = %v, %v; want false", on, err)
	}
	// An unknown sha 404s: not on the branch, not an error.
	on, err = rc.CommitOnBranch(t.Context(), "main", "4444444444444444444444444444444444444444")
	if err != nil || on {
		t.Fatalf("CommitOnBranch(unknown) = %v, %v; want false, nil", on, err)
	}
}

func TestRepoClientReleases(t *testing.T) {
	app := newFakeGitHub(t, map[string]string{
		"/repos/acme/app/releases": `[
			{"tag_name": "v2", "target_commitish": "2222222222222222222222222222222222222222", "published_at": "2026-07-21T00:00:00Z"},
			{"tag_name": "v1", "target_commitish": "main", "published_at": "2026-07-01T00:00:00Z"}
		]`,
	})
	rc, err := app.NewRepoClient(t.Context(), "acme/app")
	if err != nil {
		t.Fatalf("new repo client: %v", err)
	}
	rels, err := rc.Releases(t.Context())
	if err != nil || len(rels) != 2 || rels[0].TagName != "v2" {
		t.Fatalf("Releases = %+v, %v; want 2 releases, v2 first", rels, err)
	}
	_ = json.Valid // keep the import honest if unused elsewhere
}
```

(Remove the `json.Valid` line if `encoding/json` ends up unused.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/githubauth/ -run TestRepoClient`
Expected: FAIL — `app.NewRepoClient undefined`

- [ ] **Step 3: Write the implementation**

`internal/githubauth/repoclient.go`:

```go
// RepoClient: authenticated reads against one repo for the reconcile poll
// engine (spec 013 engine 2). One installation token is minted per repo per
// run — the spec's batching unit for rate limits.

package githubauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// RepoClient performs GitHub reads for one repo with an installation token.
type RepoClient struct {
	base string
	path string // escaped "owner/name"
	auth string
}

// NewRepoClient mints an installation token for repo and returns a client
// bound to it. Token minting failing IS the "App not installed" signal.
func (a *AppAuth) NewRepoClient(ctx context.Context, repo string) (*RepoClient, error) {
	path, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	return &RepoClient{base: a.BaseURL, path: path, auth: "Bearer " + token}, nil
}

// PRFacts is the subset of a GitHub pull request the poll engine writes
// back through store.UpsertPR — the same fields the webhook payload carries.
type PRFacts struct {
	Number         int64      `json:"number"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	Merged         bool       `json:"merged"`
	Body           string     `json:"body"`
	HTMLURL        string     `json:"html_url"`
	CreatedAt      time.Time  `json:"created_at"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA *string    `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

// HeadRef and HeadSHA give PRFacts the flat accessors the poller uses.
func (p *PRFacts) HeadRef() string { return p.Head.Ref }
func (p *PRFacts) HeadSHA() string { return p.Head.SHA }

// PR reads one pull request's current truth.
func (c *RepoClient) PR(ctx context.Context, number int64) (*PRFacts, error) {
	var pr PRFacts
	u := fmt.Sprintf("%s/repos/%s/pulls/%d", c.base, c.path, number)
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &pr)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("get PR %s#%d: status %d", c.path, number, code)
	}
	return &pr, nil
}

// DefaultBranch reads the repo's default branch name.
func (c *RepoClient) DefaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	code, err := githubJSON(ctx, http.MethodGet, c.base+"/repos/"+c.path, c.auth, &repo)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK || repo.DefaultBranch == "" {
		return "", fmt.Errorf("get repo %s: status %d", c.path, code)
	}
	return repo.DefaultBranch, nil
}

// CommitOnBranch reports whether sha is an ancestor of (i.e. contained in)
// branch, via the compare API: base=sha, head=branch — "ahead" or
// "identical" means the branch contains the sha. A 404 (unknown sha) is
// false, not an error.
func (c *RepoClient) CommitOnBranch(ctx context.Context, branch, sha string) (bool, error) {
	var cmp struct {
		Status string `json:"status"`
	}
	u := fmt.Sprintf("%s/repos/%s/compare/%s...%s",
		c.base, c.path, url.PathEscape(sha), url.PathEscape(branch))
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &cmp)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK:
		return cmp.Status == "ahead" || cmp.Status == "identical", nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("compare %s %s...%s: status %d", c.path, sha, branch, code)
	}
}

// ReleaseFacts is one published release as the poll engine consumes it.
type ReleaseFacts struct {
	TagName         string    `json:"tag_name"`
	TargetCommitish string    `json:"target_commitish"`
	PublishedAt     time.Time `json:"published_at"`
}

// Releases lists the repo's releases, newest first (GitHub's order).
// per_page=100 matches DiscoverDoneState's pagination stance.
func (c *RepoClient) Releases(ctx context.Context) ([]ReleaseFacts, error) {
	var rels []ReleaseFacts
	u := c.base + "/repos/" + c.path + "/releases?per_page=100"
	code, err := githubJSON(ctx, http.MethodGet, u, c.auth, &rels)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("list releases %s: status %d", c.path, code)
	}
	return rels, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/githubauth/... -v -run TestRepoClient`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/githubauth/repoclient.go internal/githubauth/repoclient_test.go
git commit -m "Add per-repo installation-token GitHub reads for reconcile"
```

---

## Task 11: Poll-candidate store queries

**Files:**
- Modify: `internal/store/reconcile.go` (append)
- Test: `internal/store/reconcile_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/reconcile_test.go`:

```go
func TestPollCandidates(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}

	// Seed through RecordEvent: Transition logs to state_log, whose event_id
	// is a NOT NULL FK to events (0001_baseline.up.sql:177), so it needs a
	// real event id.
	var inReview, merged string
	if _, _, err := s.RecordEvent(ctx, "cli", "seed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := s.Now()
			t1, err := CreateTask(tx, now, TaskInput{ProjectID: "demo", Title: "a", Priority: "medium", Kind: "bug"})
			if err != nil {
				return err
			}
			inReview = t1.ID
			t2, err := CreateTask(tx, now, TaskInput{ProjectID: "demo", Title: "b", Priority: "medium", Kind: "bug"})
			if err != nil {
				return err
			}
			merged = t2.ID
			// t1: in_review with an open PR. t2: only a task commit, ready.
			if err := Transition(tx, now, inReview, "ready", "in_progress", eventID); err != nil {
				return err
			}
			if err := Transition(tx, now, inReview, "in_progress", "in_review", eventID); err != nil {
				return err
			}
			if _, err := UpsertPR(tx, PullRequest{
				Repo: "acme/app", Number: 12, Title: "fix", State: "open",
				HeadRef: "lode/" + inReview + "-fix",
				HeadSHA: "1111111111111111111111111111111111111111",
				URL:     "u", OpenedAt: now,
			}, ""); err != nil {
				return err
			}
			return InsertTaskCommit(tx, TaskCommit{
				TaskID: merged, Repo: "acme/app",
				SHA: "5555555555555555555555555555555555555555", Source: "pr", SeenAt: now,
			})
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	all, err := s.PollCandidates(ctx, "", "", nil)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("candidates = %+v; want both tasks", all)
	}

	one, err := s.PollCandidates(ctx, "", inReview, nil)
	if err != nil {
		t.Fatalf("task-bounded: %v", err)
	}
	if len(one) != 1 || one[0].TaskID != inReview || one[0].Repo != "acme/app" {
		t.Fatalf("task-bounded = %+v; want only %s", one, inReview)
	}

	none, err := s.PollCandidates(ctx, "other/repo", "", nil)
	if err != nil {
		t.Fatalf("repo-bounded: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("repo-bounded = %+v; want none", none)
	}

	unlanded, err := s.UnlandedTaskCommits(ctx, merged, "acme/app")
	if err != nil {
		t.Fatalf("unlanded: %v", err)
	}
	if len(unlanded) != 1 || unlanded[0] != "5555555555555555555555555555555555555555" {
		t.Fatalf("unlanded = %v; want the seeded sha", unlanded)
	}
	// Once the sha is on main, it is no longer unlanded.
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := AppendMainCommit(tx, "acme/app", "5555555555555555555555555555555555555555", s.Now())
		return err
	}); err != nil {
		t.Fatalf("append main commit: %v", err)
	}
	unlanded, err = s.UnlandedTaskCommits(ctx, merged, "acme/app")
	if err != nil {
		t.Fatalf("unlanded after landing: %v", err)
	}
	if len(unlanded) != 0 {
		t.Fatalf("unlanded after landing = %v; want none", unlanded)
	}
}
```

If `UpsertPR`'s branch correlation does not attribute the PR to `inReview`
via the `lode/<id>-` head ref, check `internal/store/changes.go:151` for the
correlation rule it actually applies (branch prefix vs. body marker) and
adjust the seeded `HeadRef`/body accordingly — the store is the authority,
not this test.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestPollCandidates`
Expected: FAIL — `undefined: (*Store).PollCandidates`

- [ ] **Step 3: Write the implementation**

Append to `internal/store/reconcile.go`:

```go
// PollCandidate is one (task, repo) pair the poll engine should ask GitHub
// about.
type PollCandidate struct {
	TaskID string
	State  string
	Repo   string
}

// PollCandidates returns tasks whose delivery state can still advance
// (the same advanceable set TasksBelowFrontier uses) paired with each repo
// they have recorded activity in — a PR or a task commit; a task with
// neither has nothing to poll. repo/task/since bound the set (spec 013);
// since compares tasks.updated_at against the server clock.
//
// Spec 013 open question 1: this set may be too large for an unscoped
// org-wide run; --since/--repo are the intended controls.
func (s *Store) PollCandidates(ctx context.Context, repo, task string, since *time.Time) ([]PollCandidate, error) {
	q := `SELECT DISTINCT t.id, t.state, x.repo
	      FROM tasks t
	      JOIN (SELECT task_id, repo FROM pull_requests WHERE task_id IS NOT NULL
	            UNION
	            SELECT task_id, repo FROM task_commits) x ON x.task_id = t.id
	      WHERE t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`
	var args []any
	if repo != "" {
		args = append(args, repo)
		q += fmt.Sprintf(` AND x.repo = $%d`, len(args))
	}
	if task != "" {
		args = append(args, task)
		q += fmt.Sprintf(` AND t.id = $%d`, len(args))
	}
	if since != nil {
		args = append(args, since.UTC())
		q += fmt.Sprintf(` AND t.updated_at >= $%d`, len(args))
	}
	q += ` ORDER BY t.id, x.repo`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("poll candidates: %w", err)
	}
	defer rows.Close()

	var out []PollCandidate
	for rows.Next() {
		var c PollCandidate
		if err := rows.Scan(&c.TaskID, &c.State, &c.Repo); err != nil {
			return nil, fmt.Errorf("scan poll candidate: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("poll candidates: %w", err)
	}
	return out, nil
}

// UnlandedTaskCommits returns a task's recorded commit shas in repo that are
// not yet known to be on the default branch (absent from main_commits),
// sorted. These are what the poll engine checks against GitHub.
func (s *Store) UnlandedTaskCommits(ctx context.Context, taskID, repo string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tc.sha FROM task_commits tc
		 WHERE tc.task_id = $1 AND tc.repo = $2
		   AND NOT EXISTS (SELECT 1 FROM main_commits mc
		                   WHERE mc.repo = tc.repo AND mc.sha = tc.sha)
		 ORDER BY tc.sha`, taskID, repo)
	if err != nil {
		return nil, fmt.Errorf("unlanded commits for %s in %s: %w", taskID, repo, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("scan unlanded commit: %w", err)
		}
		out = append(out, sha)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("unlanded commits for %s in %s: %w", taskID, repo, err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -run TestPollCandidates -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Add poll-candidate queries for reconcile engine 2"
```

---

## Task 12: Engine 2 — poll GitHub

**Files:**
- Create: `internal/reconcile/poll.go`
- Test: `internal/reconcile/poll_test.go`

- [ ] **Step 1: Write the failing test**

`internal/reconcile/poll_test.go` (`package reconcile_test`; Postgres via
`store.OpenTestStore`, GitHub via the Task 10 fake — copy `newFakeGitHub`
here or lift it into a shared exported test helper only if `githubauth`
already exports one; do not export new production API for tests):

```go
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
			})
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
				HeadRef: "lode/" + taskID + "-fix", HeadSHA: headSHA,
				URL: "u", OpenedAt: now,
			}, "")
			return err
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return taskID
}

func mergedPRRoutes() map[string]string {
	return map[string]string{
		"/repos/acme/app": `{"default_branch": "main"}`,
		"/repos/acme/app/pulls/12": `{
			"number": 12, "title": "fix", "state": "closed", "merged": true,
			"body": "", "html_url": "u",
			"merge_commit_sha": "` + mergeSHA + `",
			"merged_at": "2026-07-20T10:00:00Z", "created_at": "2026-07-19T09:00:00Z",
			"head": {"ref": "lode/WL-1-fix", "sha": "` + headSHA + `"}
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
	app := newFakeGitHub(t, mergedPRRoutes())
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

func TestPollDryRunReportsWithoutWriting(t *testing.T) {
	st := store.OpenTestStore(t)
	taskID := seedStaleTask(t, st)
	app := newFakeGitHub(t, mergedPRRoutes())

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
	ev, err := st.EventByID(context.Background(), id)
	if err != nil {
		t.Fatalf("event %d: %v", id, err)
	}
	return *ev
}
```

Add the small read `EventByID` to `internal/store/reconcile.go` (it is
generally useful for attribution checks and the timeline):

```go
// EventByID returns one event row.
func (s *Store) EventByID(ctx context.Context, id int64) (*Event, error) {
	var e Event
	err := s.db.QueryRowContext(ctx,
		`SELECT id, source, external_id, type, payload, received_at
		 FROM events WHERE id = $1`, id).
		Scan(&e.ID, &e.Source, &e.ExternalID, &e.Type, &e.Payload, &e.ReceivedAt)
	if err != nil {
		return nil, fmt.Errorf("event %d: %w", id, err)
	}
	e.ReceivedAt = e.ReceivedAt.UTC()
	return &e, nil
}
```

As in Task 11, verify the seeded `HeadRef` actually correlates the PR to the
task under `UpsertPR`'s rules before trusting a failure.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/reconcile/...`
Expected: FAIL — `no required module provides package .../internal/reconcile`

- [ ] **Step 3: Write the poller**

`internal/reconcile/poll.go`:

```go
// Package reconcile implements engine 2 of lode reconcile (spec 013): ask
// GitHub the current truth about candidate tasks, write the missing facts
// through the existing upserts, and let store.ResolveDelivery advance the
// state. Because ResolveDelivery derives delivery state from recorded facts,
// repairing facts is sufficient — no event ordering to replay.
//
// Two phases per run: gather (network reads, no writes) then apply (one
// store.RecordEvent transaction under a single source='system' event of type
// "reconcile.poll", external_id = run id). Facts and transitions attribute
// to that event: the task advanced because reconcile observed it.
package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/githubauth"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Options bound one poll run. RunID is the system event's external_id and
// must be unique per run.
type Options struct {
	Repo   string
	Task   string
	Since  *time.Time
	DryRun bool
	RunID  string
}

// TaskRepair is what the run did (or would do) for one task.
type TaskRepair struct {
	TaskID        string   `json:"task_id"`
	Repo          string   `json:"repo"`
	State         string   `json:"state"` // state before the run
	PRsUpdated    []int64  `json:"prs_updated,omitempty"`
	CommitsLanded []string `json:"commits_landed,omitempty"`
}

// PollResult is one run's report.
type PollResult struct {
	RunID      string       `json:"run_id"`
	DryRun     bool         `json:"dry_run"`
	Candidates int          `json:"candidates"`
	Repaired   []TaskRepair `json:"repaired"`
	Errors     []string     `json:"errors,omitempty"`
}

// repoFacts is everything gathered for one repo before the apply phase.
type repoFacts struct {
	repo          string
	prs           []store.PullRequest // fresh facts, ready for UpsertPR
	prBodies      map[int64]string
	landedSHAs    []string // shas GitHub confirms are on the default branch
	mergedCommits []store.TaskCommit
	releases      []githubauth.ReleaseFacts
	tasks         []store.PollCandidate
}

// Poll runs engine 2. app must be non-nil; the API layer skips polling (with
// an explanation) when the GitHub App is not configured.
func Poll(ctx context.Context, st *store.Store, app *githubauth.AppAuth, opts Options) (*PollResult, error) {
	candidates, err := st.PollCandidates(ctx, opts.Repo, opts.Task, opts.Since)
	if err != nil {
		return nil, err
	}
	res := &PollResult{RunID: opts.RunID, DryRun: opts.DryRun, Candidates: len(candidates)}
	if len(candidates) == 0 {
		return res, nil
	}

	byRepo := map[string][]store.PollCandidate{}
	for _, c := range candidates {
		byRepo[c.Repo] = append(byRepo[c.Repo], c)
	}

	var gathered []*repoFacts
	for repo, tasks := range byRepo {
		facts, err := gatherRepo(ctx, st, app, repo, tasks)
		if err != nil {
			// One repo failing (App not installed there, rate limit) must not
			// abort the run for every other repo.
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		gathered = append(gathered, facts)
	}

	for _, f := range gathered {
		for _, c := range f.tasks {
			repair := TaskRepair{TaskID: c.TaskID, Repo: f.repo, State: c.State}
			for _, pr := range f.prs {
				if pr.TaskID != nil && *pr.TaskID == c.TaskID {
					repair.PRsUpdated = append(repair.PRsUpdated, pr.Number)
				}
			}
			repair.CommitsLanded = f.landedSHAs
			if len(repair.PRsUpdated) > 0 || len(repair.CommitsLanded) > 0 {
				res.Repaired = append(res.Repaired, repair)
			}
		}
	}
	if opts.DryRun || len(res.Repaired) == 0 {
		return res, nil
	}

	summary, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encode run summary: %w", err)
	}
	_, _, err = st.RecordEvent(ctx, "system", opts.RunID, "reconcile.poll", summary,
		func(tx *sql.Tx, eventID int64) error {
			return applyFacts(tx, st.Now(), eventID, gathered)
		})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// gatherRepo reads GitHub once per repo: one installation token, then the
// PRs, default-branch membership, and releases for that repo's candidate
// tasks. Read-only.
func gatherRepo(ctx context.Context, st *store.Store, app *githubauth.AppAuth, repo string, tasks []store.PollCandidate) (*repoFacts, error) {
	rc, err := app.NewRepoClient(ctx, repo)
	if err != nil {
		return nil, err
	}
	defaultBranch, err := rc.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}

	f := &repoFacts{repo: repo, prBodies: map[int64]string{}, tasks: tasks}
	now := st.Now()
	shasToCheck := map[string]bool{}

	for _, c := range tasks {
		prs, err := st.PRsForTask(ctx, c.TaskID)
		if err != nil {
			return nil, err
		}
		for _, known := range prs {
			if known.Repo != repo {
				continue
			}
			gh, err := rc.PR(ctx, known.Number)
			if err != nil {
				return nil, err
			}
			state := "open"
			if gh.State == "closed" {
				state = "closed"
				if gh.Merged {
					state = "merged"
				}
			}
			openedAt := gh.CreatedAt
			if openedAt.IsZero() {
				openedAt = now
			}
			taskID := c.TaskID
			f.prs = append(f.prs, store.PullRequest{
				Repo: repo, Number: gh.Number, Title: gh.Title, State: state,
				TaskID: &taskID, HeadRef: gh.HeadRef(), HeadSHA: gh.HeadSHA(),
				MergeSHA: gh.MergeCommitSHA, URL: gh.HTMLURL,
				OpenedAt: openedAt, MergedAt: gh.MergedAt,
			})
			f.prBodies[gh.Number] = gh.Body
			if gh.Merged {
				if sha := gh.HeadSHA(); sha != "" {
					f.mergedCommits = append(f.mergedCommits, store.TaskCommit{
						TaskID: c.TaskID, Repo: repo, SHA: sha, Source: "pr", SeenAt: now,
					})
				}
				if gh.MergeCommitSHA != nil && *gh.MergeCommitSHA != "" {
					f.mergedCommits = append(f.mergedCommits, store.TaskCommit{
						TaskID: c.TaskID, Repo: repo, SHA: *gh.MergeCommitSHA, Source: "pr", SeenAt: now,
					})
					shasToCheck[*gh.MergeCommitSHA] = true
				}
			}
		}
		// Commits the backbone recorded that never showed up on main.
		unlanded, err := st.UnlandedTaskCommits(ctx, c.TaskID, repo)
		if err != nil {
			return nil, err
		}
		for _, sha := range unlanded {
			shasToCheck[sha] = true
		}
	}

	for sha := range shasToCheck {
		on, err := rc.CommitOnBranch(ctx, defaultBranch, sha)
		if err != nil {
			return nil, err
		}
		if on {
			f.landedSHAs = append(f.landedSHAs, sha)
		}
	}

	// Releases only matter for release-terminated repos; asking costs one
	// request and applyFacts ignores unresolvable ones, so ask uniformly.
	rels, err := rc.Releases(ctx)
	if err != nil {
		return nil, err
	}
	f.releases = rels
	return f, nil
}

// applyFacts writes one run's gathered facts inside the reconcile.poll
// event's transaction: PR upserts, task commits, main-branch appends,
// release frontiers, then ResolveDelivery per candidate. Every write is an
// upsert or a from-state-guarded transition, so a re-run converges.
func applyFacts(tx *sql.Tx, now time.Time, eventID int64, gathered []*repoFacts) error {
	for _, f := range gathered {
		for _, pr := range f.prs {
			if _, err := store.UpsertPR(tx, pr, f.prBodies[pr.Number]); err != nil {
				return err
			}
		}
		for _, tc := range f.mergedCommits {
			if err := store.InsertTaskCommit(tx, tc); err != nil {
				return err
			}
		}
		for _, sha := range f.landedSHAs {
			// Guarded: only append shas main_commits does not already know,
			// so re-running never duplicates the frontier.
			known, err := store.MainIDForSHA(tx, f.repo, sha)
			if err != nil {
				return err
			}
			if known == nil {
				if _, err := store.AppendMainCommit(tx, f.repo, sha, now); err != nil {
					return err
				}
			}
		}
		for _, rel := range f.releases {
			mainID, err := store.MainIDForSHA(tx, f.repo, rel.TargetCommitish)
			if err != nil {
				return err
			}
			if mainID == nil {
				// target_commitish is often a branch name; without a
				// resolvable sha there is no frontier to record. Conservative:
				// skip rather than guess (the webhook path's LatestMainID
				// fallback is only correct at delivery time).
				continue
			}
			publishedAt := rel.PublishedAt
			if publishedAt.IsZero() {
				publishedAt = now
			}
			if err := store.SetReleaseFrontier(tx, f.repo, rel.TagName, *mainID, publishedAt); err != nil {
				return err
			}
		}
		for _, c := range f.tasks {
			if err := store.ResolveDelivery(tx, now, c.TaskID, f.repo, eventID); err != nil {
				return err
			}
		}
	}
	return nil
}
```

If `store.SetReleaseFrontier` is not forward-only on re-runs, check its body
(`internal/store/delivery.go:280`) before assuming — the convergence test
will catch a regression either way.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/reconcile/... -v`
Expected: PASS (2 tests). The convergence assertion is the important one: a
second run must add no state_log entries.

- [ ] **Step 5: Commit**

```bash
git add internal/reconcile internal/store/reconcile.go internal/store/reconcile_test.go
git commit -m "Poll GitHub for missed delivery facts (reconcile engine 2)"
```

---

## Task 13: Wire engine 2 into the endpoint; finish the surface

**Files:**
- Modify: `internal/api/reconcile.go` (poll wiring)
- Modify: `README.md`
- Test: `internal/api/reconcile_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/reconcile_test.go`:

```go
// TestReconcilePollSkippedWithoutApp: with no GitHub App configured the
// endpoint still runs replay and says why polling did not happen.
func TestReconcilePollSkippedWithoutApp(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/reconcile", token, map[string]any{"dry_run": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Poll        json.RawMessage `json:"poll"`
		PollSkipped string          `json:"poll_skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(resp.Poll) != "null" || resp.PollSkipped == "" {
		t.Fatalf("poll = %s, skipped = %q; want null + an explanation", resp.Poll, resp.PollSkipped)
	}
}
```

A full-stack poll-through-the-endpoint test needs an `api.NewServer` built
with a fake App key against a fake GitHub — the poll behavior itself is
already covered in `internal/reconcile/poll_test.go`, so the API layer test
only asserts the wiring branch. If `newTestServer` cannot be parameterized
with an `api.Config` without churn, this skipped-branch test plus the
poll-package tests are sufficient; do not rebuild the server fixture for one
assertion.

- [ ] **Step 2: Wire the poller in**

In `internal/api/reconcile.go`, replace the Task 9 (part 2) placeholder tail of
`reconcile` (the `resp.PollSkipped = ...` line and the guard) with:

```go
	if s.appAuth == nil {
		resp.PollSkipped = "github app auth not configured (LODE_GITHUB_APP_ID / LODE_GITHUB_APP_PRIVATE_KEY)"
	} else {
		poll, err := reconcile.Poll(r.Context(), s.st, s.appAuth, reconcile.Options{
			Repo: req.Repo, Task: req.Task, Since: since, DryRun: req.DryRun, RunID: runID,
			Log: s.log, Metrics: s.pollMetrics,
		})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		resp.Poll = poll
	}
```

with `"github.com/sunstoneinstitute/worklode/internal/reconcile"` in the
imports. `s.pollMetrics` is a new `*reconcile.Metrics` field on `server`, set
in `registerRoutes` from `reconcile.NewMetrics(reg)` next to the
`hooks.NewMetrics(reg)` line — engine 2's instruments already exist
(`internal/reconcile/metrics.go`, WL-200); this task is only their
registerer. Then tighten the response type:

```go
	Poll *reconcile.PollResult `json:"poll"`
```

- [ ] **Step 3: Run the API and cmd suites**

Run: `go test ./internal/api/... ./internal/cmd/...`
Expected: PASS — the `lode reconcile` renderer from Task 9 (part 2) already prints
the poll section when present.

- [ ] **Step 4: Document the commands**

In `README.md`, in the CLI command section, add three short entries (match
the file's existing style and brevity):

- `lode doctor` — client-side setup checks; exits non-zero on any failure
  and names each fix; works with the server unreachable.
- `lode project doctor [repo]` — per-repo webhook-ingestion health
  (admin): App installation, last delivery, unapplied events, unmapped
  senders; a stale repo is the cue to run reconcile.
- `lode reconcile [--repo X | --task Y] [--since D] [--dry-run]` — repair
  what ingestion missed (admin): replays stored `*.ignored` events, then
  polls GitHub for missed PR/merge/release facts; `--since` takes RFC 3339
  or a Go duration against the server clock.

- [ ] **Step 5: Full verification**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS across the repo (Postgres required for the store-backed
suites, as usual).

- [ ] **Step 6: Commit**

```bash
git add internal/api README.md
git commit -m "Run the poll engine from POST /api/v1/reconcile"
```

---

## Acceptance criteria → tasks

| Spec acceptance criterion | Covered by |
|---|---|
| Poll advances a merged-while-down task; attribution to `reconcile.poll`; second run a no-op; `--dry-run` reports the same repair, writes nothing | Task 12 tests |
| Deterministic `--json` on every command | root `--json` + sorted store queries (`ORDER BY` in every reconcile query) |
