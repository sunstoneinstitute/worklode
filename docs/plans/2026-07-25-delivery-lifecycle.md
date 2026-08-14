---
status: superseded
covers: docs/specs/004-execution-backbone.md
---
# Delivery Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the delivery lifecycle from `docs/specs/004-execution-backbone.md`: rename `done` → `merged`, add `deployed_dev`/`deployed_prod`/`released` states, and advance tasks automatically from GitHub `push`/`deployment_status`/`release` events plus Flux reconciliation confirmations.

**Architecture:** Webhook handlers record *facts* (task↔commit attribution, main-commit ordering, per-environment deploy watermarks, release frontiers) inside the same `RecordEvent` transaction as today, then call one shared resolver (`store.ResolveDelivery`) that advances the task to the furthest milestone the facts support. Forward-only, idempotent, arrival-order independent.

**Tech Stack:** Go 1.26, Postgres (golang-migrate SQL files), net/http mux, go-jose (GitHub App JWT). Tests need Postgres from `docker-compose.yml` (`store.OpenTestStore` skips if unreachable).

**Read first:** `docs/specs/004-execution-backbone.md` (the spec), `internal/hooks/github.go` (handler pattern), `internal/store/events.go:34` (`RecordEvent`), `internal/store/tasks.go:58-77` (`legalTransitions`).

**Conventions:** run `go test ./internal/...` for tests; commit after every task; commit messages in imperative mood without any Co-authored-by/advertising trailers.

---

### Task 1: Migration 0004 — schema for delivery lifecycle

**Files:**
- Create: `deploy/base/migrations/0004_delivery.up.sql`
- Create: `deploy/base/migrations/0004_delivery.down.sql`
- Modify: `deploy/base/kustomization.yaml` (configMapGenerator file list)

- [ ] **Step 1: Write the up migration**

`deploy/base/migrations/0004_delivery.up.sql`:

```sql
-- Delivery lifecycle (docs/specs/004-execution-backbone.md):
-- rename done -> merged, add delivery states, fact tables, per-repo done_state.

ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'merged' WHERE state = 'done';
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN
    ('draft','ready','in_progress','in_review','merged',
     'deployed_dev','deployed_prod','released','abandoned'));

ALTER TABLE projects DROP COLUMN deploy_gated;

ALTER TABLE project_repos ADD COLUMN done_state text NOT NULL DEFAULT 'merged'
    CHECK (done_state IN ('merged','deployed_prod','released'));

-- Commits attributed to a task (from task-branch pushes, PRs, merge-commit
-- messages, or WL-Task markers on main).
CREATE TABLE task_commits (
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    repo    text NOT NULL,
    sha     text NOT NULL,
    source  text NOT NULL CHECK (source IN ('branch_push','pr','merge_message','marker')),
    seen_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, repo, sha)
);
CREATE INDEX task_commits_repo_sha ON task_commits (repo, sha);

-- Every commit pushed to a repo's default branch, in push order. The id is
-- the "seq": inclusion checks are integer comparisons per repo.
CREATE TABLE main_commits (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo      text NOT NULL,
    sha       text NOT NULL,
    pushed_at timestamptz NOT NULL,
    UNIQUE (repo, sha)
);

-- Maps a deploy-branch (last-deploy/*) commit back to the main commit its
-- main-sha: trailer names.
CREATE TABLE deploy_shas (
    repo    text NOT NULL,
    sha     text NOT NULL,
    main_id bigint NOT NULL REFERENCES main_commits(id) ON DELETE CASCADE,
    PRIMARY KEY (repo, sha)
);

-- Per-environment deployed frontier: forward-only watermarks per signal.
-- flux_seen latches on the first correlated Flux event; before that the
-- GitHub signal alone confirms (bootstrap fallback per spec).
CREATE TABLE env_deploys (
    repo         text NOT NULL,
    environment  text NOT NULL CHECK (environment IN ('dev','prod')),
    gh_main_id   bigint REFERENCES main_commits(id) ON DELETE SET NULL,
    flux_main_id bigint REFERENCES main_commits(id) ON DELETE SET NULL,
    flux_seen    boolean NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (repo, environment)
);

-- Latest main commit covered by each published release.
CREATE TABLE release_frontiers (
    repo         text NOT NULL,
    tag          text NOT NULL,
    main_id      bigint NOT NULL REFERENCES main_commits(id) ON DELETE CASCADE,
    published_at timestamptz NOT NULL,
    PRIMARY KEY (repo, tag)
);
```

- [ ] **Step 2: Write the down migration**

`deploy/base/migrations/0004_delivery.down.sql`:

```sql
DROP TABLE release_frontiers;
DROP TABLE env_deploys;
DROP TABLE deploy_shas;
DROP TABLE main_commits;
DROP TABLE task_commits;

ALTER TABLE project_repos DROP COLUMN done_state;

ALTER TABLE projects ADD COLUMN deploy_gated boolean NOT NULL DEFAULT false;

ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'done'
    WHERE state IN ('merged','deployed_dev','deployed_prod','released');
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN
    ('draft','ready','in_progress','in_review','done','abandoned'));
```

- [ ] **Step 3: Register the files in kustomize**

In `deploy/base/kustomization.yaml`, extend the `worklode-migrations` configMapGenerator file list (after the 0003 entries):

```yaml
      - migrations/0004_delivery.up.sql
      - migrations/0004_delivery.down.sql
```

- [ ] **Step 4: Verify migrations apply**

Run: `go test ./internal/store/ -run TestStore -count=1`
(`OpenTestStore` applies all migrations to a fresh DB — a broken SQL file fails every store test.)
Expected: PASS (or SKIP if Postgres is down — then `docker compose up -d postgres` first).

**Note:** the constraint name `tasks_state_check` is Postgres's default for the inline CHECK in `0001_baseline.up.sql`. If `DROP CONSTRAINT` fails, find the real name with `SELECT conname FROM pg_constraint WHERE conrelid = 'tasks'::regclass AND contype = 'c';` and adjust.

- [ ] **Step 5: Commit**

```bash
git add deploy/base/migrations/0004_delivery.up.sql deploy/base/migrations/0004_delivery.down.sql deploy/base/kustomization.yaml
git commit -m "Add migration 0004: delivery lifecycle schema"
```

---

### Task 2: Rename state `done` → `merged` across the codebase

The migration renamed the DB rows; this task renames the Go/state-machine side. The CLI command `lode task done` and endpoint `POST /tasks/{id}/done` KEEP their names (manual escape hatch); only the resulting **state** is renamed.

**Files:**
- Modify: `internal/store/tasks.go` (legalTransitions + comment)
- Modify: `internal/api/lifecycle.go` (doneTask, reopenTask)
- Modify: `internal/hooks/github.go` (in_review→done transition — becomes in_review→merged for now; reworked fully in Task 8)
- Modify: `internal/cmd/task.go` (help text, default list filter)
- Modify: `internal/api/templates/layout.html` (badge CSS)
- Modify: `internal/api/templates/project.html`, `internal/api/templates/board.html` (badge class bug)
- Modify: every `_test.go` that asserts state `"done"`

- [ ] **Step 1: Find every reference**

Run: `grep -rn '"done"' internal/ | grep -v _test` and `grep -rn '"done"' internal/ | grep _test`
Keep the list; every hit is either a state string (rename to `"merged"`) or a command/endpoint/event name (`task.done`, `Use: "done <id>"`, route `/done` — keep).

- [ ] **Step 2: Update legalTransitions in `internal/store/tasks.go`**

Replace the map (lines 58-77) with (delivery transitions included now so Task 6's resolver has them; reopen widened):

```go
// legalTransitions is the complete task state machine: draft → ready →
// in_progress → in_review → merged → deployed_dev → deployed_prod, with
// released as the terminal for release-based repos, backward moves
// in_progress → ready and in_review → in_progress, direct-to-main jumps
// ready|in_progress → merged, and abandoned reachable from every
// pre-merged state. Terminal-ish states are not strictly terminal: reopen
// returns to ready (a fresh claim is then required).
var legalTransitions = map[[2]string]bool{
	{"draft", "ready"}:               true,
	{"ready", "in_progress"}:         true,
	{"in_progress", "in_review"}:     true,
	{"in_progress", "ready"}:         true,
	{"in_review", "in_progress"}:     true,
	{"ready", "merged"}:              true,
	{"in_progress", "merged"}:        true,
	{"in_review", "merged"}:          true,
	{"merged", "deployed_dev"}:       true,
	{"merged", "deployed_prod"}:      true,
	{"merged", "released"}:           true,
	{"deployed_dev", "deployed_prod"}: true,
	{"deployed_dev", "released"}:     true,
	{"draft", "abandoned"}:           true,
	{"ready", "abandoned"}:           true,
	{"in_progress", "abandoned"}:     true,
	{"in_review", "abandoned"}:       true,
	{"merged", "ready"}:              true,
	{"deployed_dev", "ready"}:        true,
	{"deployed_prod", "ready"}:       true,
	{"released", "ready"}:            true,
	{"abandoned", "ready"}:           true,
}
```

- [ ] **Step 3: Update `internal/api/lifecycle.go`**

- `doneTask` (line ~277): `store.Transition(tx, now, taskID, "in_review", "merged", eventID)` — keep eventType `"task.done"`.
- `reopenTask` (line ~312): replace the `cur != "done" && cur != "abandoned"` check with:

```go
	reopenable := map[string]bool{"merged": true, "deployed_dev": true,
		"deployed_prod": true, "released": true, "abandoned": true}
	if !reopenable[cur] {
		return fmt.Errorf("task %s is in state %s, not reopenable: %w",
			taskID, cur, store.ErrBadTransition)
	}
```

- [ ] **Step 4: Update `internal/hooks/github.go` interim transition**

In `applyPullRequest`, change `store.Transition(tx, now, taskID, "in_review", "done", eventID)` to `"merged"`. (Task 8 replaces this whole block; this keeps tests green meanwhile.) The `deploy_gated` guard around it: **delete now** — `project.DeployGated` no longer exists after Step 5.

- [ ] **Step 5: Remove `deploy_gated` from store/api/cli**

- `internal/store/projects.go`: remove `DeployGated` field from `Project`, drop `deploy_gated` from both SELECTs and the scan targets, delete `SetDeployGated` (lines ~92-107). Check `CreateProject`/insert for the column too.
- `internal/api/admin.go`: remove `DeployGated` from the two request/response structs (lines 32, 41) and the `SetDeployGated` call (lines ~71-78).
- `internal/cmd/project.go`: remove the `--deploy-gated` flag (lines 29, 40, 55).
- Delete/adjust any tests referencing `deploy_gated` / `DeployGated` / `SetDeployGated`.

- [ ] **Step 6: Update CLI list filter + help text (`internal/cmd/task.go`)**

- Line 87 Short: `"List tasks (delivered and abandoned are hidden unless requested with --status)"`.
- Line 109 flag help: `"filter by status: draft, ready, in_progress, in_review, merged, deployed_dev, deployed_prod, released, abandoned, or all (repeatable; default hides merged, deployed_dev, deployed_prod, released, and abandoned)"`.
- The default-hide logic (~line 115): hidden set becomes `merged, deployed_dev, deployed_prod, released, abandoned` — i.e. the default filter requests states `draft, ready, in_progress, in_review`.
- Line 234 reopen Short: `"Reopen a delivered or abandoned task (merged|deployed_dev|deployed_prod|released|abandoned -> ready; a fresh claim is then required)"`.
- Line 434 done Short: `"Mark a task merged (in_review -> merged)"`.

- [ ] **Step 7: Update web templates**

- `internal/api/templates/project.html:31` and `board.html:30`: the badge class is hardcoded `state-in_review` — fix to `state-{{.State}}`.
- `internal/api/templates/layout.html` CSS (~line 35): rename `.state-done` (if present) to `.state-merged` and add:

```css
  .state-merged { background: #cde8cd; }
  .state-deployed_dev { background: #bfe3f0; }
  .state-deployed_prod { background: #9fd6a0; }
  .state-released { background: #9fd6a0; }
```

- [ ] **Step 8: Update tests, build, run**

Rename `"done"` state assertions to `"merged"` in `internal/store/tasks_test.go`, `internal/api/*_test.go`, `internal/hooks/github_test.go`, `internal/cmd/*_test.go` (grep list from Step 1).
Run: `go build ./... && go test ./internal/... -count=1`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "Rename task state done to merged; retire deploy_gated"
```

---

### Task 3: Configurable branch prefix (default `lode/`)

**Files:**
- Modify: `internal/store/changes.go:49-63` (pattern)
- Modify: `internal/worktree/worktree.go:24` (BranchName)
- Modify: `internal/api/lifecycle.go:105,133` (claim response branch)
- Modify: `internal/api/server.go` Config + `internal/cmd/serve.go`
- Modify: `internal/cmd/lifecycle.go:145,159`, `internal/cmd/task.go:360` (CLI derives from server response)
- Test: `internal/store/changes_test.go`

- [ ] **Step 1: Write failing tests** (add to `internal/store/changes_test.go`)

```go
func TestTaskIDFromRefPrefixes(t *testing.T) {
	SetBranchPrefix("lode/")
	t.Cleanup(func() { SetBranchPrefix("lode/") })
	cases := map[string]string{
		"lode/WL-7-fix-thing": "WL-7",
		"lode/WL-7":           "WL-7",
		"wl/WL-7-fix-thing":   "WL-7", // legacy prefix still recognized
		"main":                "",
		"lode/wl-7-lower":     "",
	}
	for ref, want := range cases {
		if got := TaskIDFromRef(ref); got != want {
			t.Errorf("TaskIDFromRef(%q) = %q, want %q", ref, got, want)
		}
	}
	SetBranchPrefix("team/")
	if got := TaskIDFromRef("team/AB-3-x"); got != "AB-3" {
		t.Errorf("custom prefix: got %q, want AB-3", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run TestTaskIDFromRefPrefixes -count=1`
Expected: FAIL — `undefined: SetBranchPrefix`.

- [ ] **Step 3: Implement in `internal/store/changes.go`**

Replace `refTaskIDPattern` and `TaskIDFromRef` with:

```go
// branchPrefixPattern matches task branches "<prefix><ID>[-slug]" for the
// configured prefix plus the legacy "wl/". Rebuilt by SetBranchPrefix;
// guarded because webhook handlers read it concurrently.
var (
	branchPatternMu     sync.RWMutex
	branchPrefixPattern = buildBranchPattern("lode/")
)

func buildBranchPattern(prefix string) *regexp.Regexp {
	alts := regexp.QuoteMeta(prefix)
	if prefix != "wl/" {
		alts += "|wl/"
	}
	return regexp.MustCompile(`^(?:` + alts + `)([A-Z][A-Z0-9]*-[0-9]+)(?:-.*)?$`)
}

// SetBranchPrefix configures the task-branch prefix (LODE_BRANCH_PREFIX,
// default "lode/"). The legacy "wl/" prefix is always also recognized.
func SetBranchPrefix(prefix string) {
	if prefix == "" {
		prefix = "lode/"
	}
	branchPatternMu.Lock()
	defer branchPatternMu.Unlock()
	branchPrefixPattern = buildBranchPattern(prefix)
}

// TaskIDFromRef extracts a task id from a branch name following the
// "<prefix><task-id>-<slug>" convention (slug optional); "" if no match.
func TaskIDFromRef(ref string) string {
	branchPatternMu.RLock()
	defer branchPatternMu.RUnlock()
	m := branchPrefixPattern.FindStringSubmatch(ref)
	if m == nil {
		return ""
	}
	return m[1]
}
```

Add `"sync"` to imports.

- [ ] **Step 4: Thread the prefix through server and CLI**

- `internal/worktree/worktree.go:24`: `func BranchName(prefix, taskID, slug string) string { return prefix + taskID + "-" + slug }`; update the file's doc comment (`wl/<id>-<slug>` → `<prefix><id>-<slug>`).
- `internal/api/server.go`: add `BranchPrefix string` to `Config`; in server construction default empty to `"lode/"`, call `store.SetBranchPrefix(cfg.BranchPrefix)`, and store it on the `server` struct as `branchPrefix`.
- `internal/api/lifecycle.go:105`: `"branch": s.branchPrefix + id + "-" + SlugifyTitle(t.Title)`; line ~133 area: wherever the claim response builds `Branch`, use `s.branchPrefix`.
- `internal/cmd/serve.go`: `BranchPrefix: os.Getenv("LODE_BRANCH_PREFIX"),` in the Config literal.
- `internal/cmd/lifecycle.go:145`: derive slug without assuming prefix: `slug = resp.Branch[strings.LastIndex(resp.Branch, id+"-")+len(id)+1:]` — guard `strings.LastIndex(...) >= 0` first, else keep existing fallback. Line 159: the CLI must use `resp.Branch` directly instead of rebuilding via `worktree.BranchName` (grep callers; pass the server-provided branch down).
- `internal/cmd/task.go:360`: same — use the branch returned by the server response, not `"wl/"+...`. If that code path has no server branch available, use `worktree.BranchName("lode/", ...)` — check the surrounding code and prefer the response field.

- [ ] **Step 5: Run tests**

Run: `go build ./... && go test ./internal/store/ ./internal/worktree/ ./internal/cmd/ ./internal/api/ -count=1`
Expected: PASS (fix any `BranchName` call sites the compiler flags).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "Make task branch prefix configurable, default lode/"
```

---

### Task 4: Store — commit-attribution facts (task_commits, main_commits, deploy_shas)

**Files:**
- Create: `internal/store/delivery.go`
- Create: `internal/store/delivery_test.go`

- [ ] **Step 1: Write failing tests**

`internal/store/delivery_test.go` (use existing test helpers style — see `changes_test.go` for `OpenTestStore` + `withTx` patterns; if there's no `withTx` helper, wrap with `s.db.Begin()` / defer rollback / commit like neighboring tests):

```go
package store

import (
	"context"
	"testing"
	"time"
)

// seedDeliveryTask creates a project, repo mapping, and one ready task,
// returning the task id. Mirrors the setup helpers in tasks_test.go.
func seedDeliveryTask(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO projects (id, name, key, next_task_num) VALUES ('p1','P1','P1',2)`)
	mustExec(`INSERT INTO project_repos (project_id, repo) VALUES ('p1','acme/app')`)
	mustExec(`INSERT INTO tasks (id, project_id, title, priority, kind, state, created_at, updated_at)
	          VALUES ('P1-1','p1','t','high','feature','ready', now(), now())`)
	return "P1-1"
}

func TestMainCommitsAndLanding(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	now := time.Now()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "aaa1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	// duplicate insert is a no-op
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "aaa1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}

	id1, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := AppendMainCommit(tx, "acme/app", "aaa1", now)
	if err != nil {
		t.Fatal(err)
	}
	if id2 <= id1 {
		t.Fatalf("ids not increasing: %d then %d", id1, id2)
	}
	// re-append returns the existing id
	again, err := AppendMainCommit(tx, "acme/app", "m1", now)
	if err != nil || again != id1 {
		t.Fatalf("re-append: got %d, %v; want %d", again, err, id1)
	}

	landed, err := LandedMainID(tx, taskID, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if landed == nil || *landed != id2 {
		t.Fatalf("landed = %v, want %d", landed, id2)
	}

	// deploy sha mapping
	if err := MapDeploySHA(tx, "acme/app", "dep1", id2); err != nil {
		t.Fatal(err)
	}
	mid, err := MainIDForSHA(tx, "acme/app", "dep1")
	if err != nil || mid == nil || *mid != id2 {
		t.Fatalf("MainIDForSHA(dep1) = %v, %v; want %d", mid, err, id2)
	}
	mid, err = MainIDForSHA(tx, "acme/app", "m1")
	if err != nil || mid == nil || *mid != id1 {
		t.Fatalf("MainIDForSHA(m1) = %v, %v; want %d", mid, err, id1)
	}
	mid, err = MainIDForSHA(tx, "acme/app", "nope")
	if err != nil || mid != nil {
		t.Fatalf("MainIDForSHA(nope) = %v, %v; want nil, nil", mid, err)
	}

	repo, mid2, err := MainIDForSHAAnyRepo(tx, "dep1")
	if err != nil || mid2 == nil || repo != "acme/app" {
		t.Fatalf("MainIDForSHAAnyRepo = %q, %v, %v", repo, mid2, err)
	}
}

func TestLandedMainIDNoCommits(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	tx, _ := s.db.Begin()
	defer tx.Rollback()
	landed, err := LandedMainID(tx, taskID, "acme/app")
	if err != nil || landed != nil {
		t.Fatalf("got %v, %v; want nil, nil", landed, err)
	}
}
```

Note: check how other store tests insert projects post-WL-12 (`projects` now has `key`/`next_task_num` — copy the exact insert shape from `projects_test.go` if it differs).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run 'TestMainCommits|TestLandedMainID' -count=1`
Expected: FAIL — undefined types/functions.

- [ ] **Step 3: Implement `internal/store/delivery.go`**

```go
// Delivery-lifecycle fact tables and resolver
// (docs/specs/004-execution-backbone.md). Handlers record
// facts inside a RecordEvent transaction, then call ResolveDelivery, which
// advances the task to the furthest milestone the facts support.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TaskCommit attributes one commit to a task.
type TaskCommit struct {
	TaskID string
	Repo   string
	SHA    string
	Source string // branch_push | pr | merge_message | marker
	SeenAt time.Time
}

// InsertTaskCommit records a task↔commit attribution; duplicates are no-ops.
func InsertTaskCommit(tx *sql.Tx, tc TaskCommit) error {
	_, err := tx.Exec(
		`INSERT INTO task_commits (task_id, repo, sha, source, seen_at)
		 VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
		tc.TaskID, tc.Repo, tc.SHA, tc.Source, tc.SeenAt.UTC())
	if err != nil {
		return fmt.Errorf("insert task_commit %s %s: %w", tc.TaskID, tc.SHA, err)
	}
	return nil
}

// AppendMainCommit records one default-branch commit and returns its id
// (the per-repo ordering "seq"). Re-appending an existing sha returns the
// original id.
func AppendMainCommit(tx *sql.Tx, repo, sha string, pushedAt time.Time) (int64, error) {
	var id int64
	err := tx.QueryRow(
		`INSERT INTO main_commits (repo, sha, pushed_at) VALUES ($1, $2, $3)
		 ON CONFLICT (repo, sha) DO NOTHING RETURNING id`,
		repo, sha, pushedAt.UTC()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT id FROM main_commits WHERE repo = $1 AND sha = $2`,
			repo, sha).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("append main_commit %s %s: %w", repo, sha, err)
	}
	return id, nil
}

// MapDeploySHA maps a deploy-branch commit to the main commit its
// main-sha: trailer names; duplicates are no-ops.
func MapDeploySHA(tx *sql.Tx, repo, sha string, mainID int64) error {
	_, err := tx.Exec(
		`INSERT INTO deploy_shas (repo, sha, main_id) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`, repo, sha, mainID)
	if err != nil {
		return fmt.Errorf("map deploy_sha %s %s: %w", repo, sha, err)
	}
	return nil
}

// MainIDForSHA resolves a sha to a main-commit id for repo, checking main
// commits first, then deploy-branch mappings. nil if unknown.
func MainIDForSHA(tx *sql.Tx, repo, sha string) (*int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM main_commits WHERE repo = $1 AND sha = $2`,
		repo, sha).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT main_id FROM deploy_shas WHERE repo = $1 AND sha = $2`,
			repo, sha).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("main id for %s %s: %w", repo, sha, err)
	}
	return &id, nil
}

// MainIDForSHAAnyRepo resolves a sha with no repo context (Flux events don't
// carry one). Returns the owning repo and id, or ("", nil) if unknown.
func MainIDForSHAAnyRepo(tx *sql.Tx, sha string) (string, *int64, error) {
	var repo string
	var id int64
	err := tx.QueryRow(`SELECT repo, id FROM main_commits WHERE sha = $1 LIMIT 1`,
		sha).Scan(&repo, &id)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`SELECT repo, main_id FROM deploy_shas WHERE sha = $1 LIMIT 1`,
			sha).Scan(&repo, &id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("main id for sha %s: %w", sha, err)
	}
	return repo, &id, nil
}

// LandedMainID returns the id of the newest main commit attributed to
// taskID in repo, or nil if the task's work has not landed on main.
func LandedMainID(tx *sql.Tx, taskID, repo string) (*int64, error) {
	var id sql.NullInt64
	err := tx.QueryRow(
		`SELECT max(mc.id) FROM task_commits tc
		 JOIN main_commits mc ON mc.repo = tc.repo AND mc.sha = tc.sha
		 WHERE tc.task_id = $1 AND tc.repo = $2`, taskID, repo).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("landed main id for %s: %w", taskID, err)
	}
	if !id.Valid {
		return nil, nil
	}
	return &id.Int64, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run 'TestMainCommits|TestLandedMainID' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/delivery.go internal/store/delivery_test.go
git commit -m "Add delivery fact tables store layer: task_commits, main_commits, deploy_shas"
```

---

### Task 5: Store — env-deploy watermarks and release frontiers

**Files:**
- Modify: `internal/store/delivery.go`
- Modify: `internal/store/delivery_test.go`

- [ ] **Step 1: Write failing tests** (append to `delivery_test.go`)

```go
func TestEnvDeployFrontier(t *testing.T) {
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	id1, _ := AppendMainCommit(tx, "acme/app", "m1", now)
	id2, _ := AppendMainCommit(tx, "acme/app", "m2", now)

	// No row yet: frontier nil.
	f, err := ConfirmedFrontier(tx, "acme/app", "dev")
	if err != nil || f != nil {
		t.Fatalf("empty frontier = %v, %v", f, err)
	}

	// GH-only (flux never seen): GH signal alone confirms (bootstrap fallback).
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", id2); err != nil {
		t.Fatal(err)
	}
	f, _ = ConfirmedFrontier(tx, "acme/app", "dev")
	if f == nil || *f != id2 {
		t.Fatalf("gh-only frontier = %v, want %d", f, id2)
	}

	// First flux signal latches dual-gating: frontier = min(gh, flux).
	if err := BumpEnvDeployFlux(tx, now, "acme/app", "dev", id1); err != nil {
		t.Fatal(err)
	}
	f, _ = ConfirmedFrontier(tx, "acme/app", "dev")
	if f == nil || *f != id1 {
		t.Fatalf("dual frontier = %v, want min %d", f, id1)
	}

	// Watermarks are forward-only.
	if err := BumpEnvDeployFlux(tx, now, "acme/app", "dev", id2); err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", id1); err != nil { // stale, ignored
		t.Fatal(err)
	}
	f, _ = ConfirmedFrontier(tx, "acme/app", "dev")
	if f == nil || *f != id2 {
		t.Fatalf("forward-only frontier = %v, want %d", f, id2)
	}
}

func TestReleaseFrontier(t *testing.T) {
	s := OpenTestStore(t)
	seedDeliveryTask(t, s)
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	f, err := ReleaseFrontier(tx, "acme/app")
	if err != nil || f != nil {
		t.Fatalf("empty release frontier = %v, %v", f, err)
	}
	id1, _ := AppendMainCommit(tx, "acme/app", "m1", now)
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id1, now); err != nil {
		t.Fatal(err)
	}
	// redelivery: same tag again is a no-op
	if err := SetReleaseFrontier(tx, "acme/app", "v1.0.0", id1, now); err != nil {
		t.Fatal(err)
	}
	f, _ = ReleaseFrontier(tx, "acme/app")
	if f == nil || *f != id1 {
		t.Fatalf("release frontier = %v, want %d", f, id1)
	}
}

func TestNormalizeEnvironment(t *testing.T) {
	cases := map[string]string{
		"dev": "dev", "test": "dev", "development": "dev", "staging": "dev",
		"prod": "prod", "production": "prod", "Production": "prod",
		"copilot": "", "github-pages": "", "pypi": "", "dev-apply": "",
	}
	for in, want := range cases {
		if got := NormalizeEnvironment(in); got != want {
			t.Errorf("NormalizeEnvironment(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run 'TestEnvDeploy|TestReleaseFrontier|TestNormalizeEnvironment' -count=1`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Implement** (append to `delivery.go`; add `"strings"` import)

```go
// NormalizeEnvironment maps a GitHub environment name to the delivery stage
// it represents: "dev", "prod", or "" for environments the lifecycle
// ignores (copilot, github-pages, pypi, *-apply, ...).
func NormalizeEnvironment(name string) string {
	switch strings.ToLower(name) {
	case "dev", "test", "development", "staging":
		return "dev"
	case "prod", "production":
		return "prod"
	default:
		return ""
	}
}

func bumpEnvDeploy(tx *sql.Tx, now time.Time, repo, env, column string, mainID int64, fluxSeen bool) error {
	// column is one of two compile-time constants below — never user input.
	q := fmt.Sprintf(
		`INSERT INTO env_deploys (repo, environment, %[1]s, flux_seen, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (repo, environment) DO UPDATE SET
		   %[1]s = greatest(coalesce(env_deploys.%[1]s, 0), excluded.%[1]s),
		   flux_seen = env_deploys.flux_seen OR excluded.flux_seen,
		   updated_at = excluded.updated_at`, column)
	if _, err := tx.Exec(q, repo, env, mainID, fluxSeen, now.UTC()); err != nil {
		return fmt.Errorf("bump env_deploy %s/%s %s: %w", repo, env, column, err)
	}
	return nil
}

// BumpEnvDeployGH advances the GitHub deployment watermark for repo/env.
func BumpEnvDeployGH(tx *sql.Tx, now time.Time, repo, env string, mainID int64) error {
	return bumpEnvDeploy(tx, now, repo, env, "gh_main_id", mainID, false)
}

// BumpEnvDeployFlux advances the Flux confirmation watermark and latches
// flux_seen, switching the repo/env to dual-signal gating permanently.
func BumpEnvDeployFlux(tx *sql.Tx, now time.Time, repo, env string, mainID int64) error {
	return bumpEnvDeploy(tx, now, repo, env, "flux_main_id", mainID, true)
}

// ConfirmedFrontier returns the newest main-commit id confirmed deployed to
// repo/env: min(gh, flux) once a Flux signal has ever been correlated for
// the pair, the GitHub watermark alone before that (bootstrap fallback).
// nil if nothing is confirmed.
func ConfirmedFrontier(tx *sql.Tx, repo, env string) (*int64, error) {
	var gh, flux sql.NullInt64
	var fluxSeen bool
	err := tx.QueryRow(
		`SELECT gh_main_id, flux_main_id, flux_seen FROM env_deploys
		 WHERE repo = $1 AND environment = $2`, repo, env).Scan(&gh, &flux, &fluxSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("frontier %s/%s: %w", repo, env, err)
	}
	if !fluxSeen {
		if !gh.Valid {
			return nil, nil
		}
		return &gh.Int64, nil
	}
	if !gh.Valid || !flux.Valid {
		return nil, nil
	}
	confirmed := min(gh.Int64, flux.Int64)
	return &confirmed, nil
}

// SetReleaseFrontier records the newest main commit covered by a published
// release; redelivery of the same tag is a no-op.
func SetReleaseFrontier(tx *sql.Tx, repo, tag string, mainID int64, publishedAt time.Time) error {
	_, err := tx.Exec(
		`INSERT INTO release_frontiers (repo, tag, main_id, published_at)
		 VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		repo, tag, mainID, publishedAt.UTC())
	if err != nil {
		return fmt.Errorf("set release frontier %s %s: %w", repo, tag, err)
	}
	return nil
}

// ReleaseFrontier returns the newest released main-commit id for repo, or
// nil if the repo has no releases recorded.
func ReleaseFrontier(tx *sql.Tx, repo string) (*int64, error) {
	var id sql.NullInt64
	err := tx.QueryRow(`SELECT max(main_id) FROM release_frontiers WHERE repo = $1`,
		repo).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("release frontier %s: %w", repo, err)
	}
	if !id.Valid {
		return nil, nil
	}
	return &id.Int64, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run 'TestEnvDeploy|TestReleaseFrontier|TestNormalizeEnvironment' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/delivery.go internal/store/delivery_test.go
git commit -m "Add env-deploy watermarks and release frontiers to store"
```

---

### Task 6: Store — the delivery resolver

**Files:**
- Modify: `internal/store/delivery.go`
- Modify: `internal/store/delivery_test.go`

- [ ] **Step 1: Write failing tests** (append; the key property: same facts in any arrival order → same outcome)

```go
// deliveryTestState reads a task's state directly.
func taskStateForTest(t *testing.T, tx *sql.Tx, id string) string {
	t.Helper()
	var st string
	if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestResolveDeliveryFullFlow(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = 'deployed_prod' WHERE repo = 'acme/app'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	// Not landed: no-op.
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "ready" {
		t.Fatalf("state = %s, want ready", st)
	}

	// Land the work.
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "branch_push", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, _ := AppendMainCommit(tx, "acme/app", "c1", now)
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "merged" {
		t.Fatalf("state = %s, want merged", st)
	}

	// Dev deploy confirmed (gh only, flux never seen) → deployed_dev.
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_dev" {
		t.Fatalf("state = %s, want deployed_dev", st)
	}

	// Prod deploy → deployed_prod (terminal for done_state=deployed_prod).
	if err := BumpEnvDeployGH(tx, now, "acme/app", "prod", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
		t.Fatalf("state = %s, want deployed_prod", st)
	}
	// Idempotent re-resolve.
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDeliveryOutOfOrderCatchUp(t *testing.T) {
	// All facts arrive before any resolve: one call walks
	// ready → merged → deployed_dev → deployed_prod.
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, _ := AppendMainCommit(tx, "acme/app", "c1", now)
	if err := BumpEnvDeployGH(tx, now, "acme/app", "dev", mid); err != nil {
		t.Fatal(err)
	}
	if err := BumpEnvDeployGH(tx, now, "acme/app", "prod", mid); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "deployed_prod" {
		t.Fatalf("state = %s, want deployed_prod", st)
	}
}

func TestResolveDeliveryReleased(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = 'released' WHERE repo = 'acme/app'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()

	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "marker", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, _ := AppendMainCommit(tx, "acme/app", "c1", now)
	if err := SetReleaseFrontier(tx, "acme/app", "v1", mid, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "released" {
		t.Fatalf("state = %s, want released", st)
	}
}

func TestResolveDeliveryNeverAdvancesDraft(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET state = 'draft' WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "marker", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	AppendMainCommit(tx, "acme/app", "c1", now)
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "draft" {
		t.Fatalf("state = %s, want draft (resolver must not touch drafts)", st)
	}
}

func TestResolveDeliveryReleaseIgnoredForServiceRepo(t *testing.T) {
	// done_state=deployed_prod (default merged here): a release event must
	// NOT move the task to released.
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s) // done_state defaults to 'merged'
	now := time.Now()
	tx, _ := s.db.Begin()
	defer tx.Rollback()
	if err := InsertTaskCommit(tx, TaskCommit{TaskID: taskID, Repo: "acme/app", SHA: "c1", Source: "pr", SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	mid, _ := AppendMainCommit(tx, "acme/app", "c1", now)
	if err := SetReleaseFrontier(tx, "acme/app", "v1", mid, now); err != nil {
		t.Fatal(err)
	}
	if err := ResolveDelivery(tx, now, taskID, "acme/app", 0); err != nil {
		t.Fatal(err)
	}
	if st := taskStateForTest(t, tx, taskID); st != "merged" {
		t.Fatalf("state = %s, want merged (released gated on done_state)", st)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run TestResolveDelivery -count=1`
Expected: FAIL — `undefined: ResolveDelivery`.

- [ ] **Step 3: Implement** (append to `delivery.go`)

```go
// RepoDoneState returns the done_state configured on the repo mapping
// ("merged" default). Unmapped repos return "merged".
func RepoDoneState(tx *sql.Tx, repo string) (string, error) {
	var st string
	err := tx.QueryRow(`SELECT done_state FROM project_repos WHERE repo = $1`,
		repo).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "merged", nil
	}
	if err != nil {
		return "", fmt.Errorf("done_state for %s: %w", repo, err)
	}
	return st, nil
}

// TasksBelowFrontier returns ids of tasks whose landed main commit in repo
// is at or below frontier and whose state can still advance. Used by
// frontier-moving handlers to find affected tasks.
func TasksBelowFrontier(tx *sql.Tx, repo string, frontier int64) ([]string, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT tc.task_id FROM task_commits tc
		 JOIN main_commits mc ON mc.repo = tc.repo AND mc.sha = tc.sha
		 JOIN tasks t ON t.id = tc.task_id
		 WHERE tc.repo = $1 AND mc.id <= $2
		   AND t.state IN ('ready','in_progress','in_review','merged','deployed_dev')`,
		repo, frontier)
	if err != nil {
		return nil, fmt.Errorf("tasks below frontier %s/%d: %w", repo, frontier, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ResolveDelivery advances taskID to the furthest delivery milestone the
// recorded facts support, forward-only, closing any active lease when the
// work first lands. All lifecycle rules live here; webhook handlers only
// record facts and call this. Safe to call repeatedly and in any
// fact-arrival order. It never advances a draft or abandoned task.
func ResolveDelivery(tx *sql.Tx, now time.Time, taskID, repo string, eventID int64) error {
	landed, err := LandedMainID(tx, taskID, repo)
	if err != nil {
		return err
	}
	if landed == nil {
		return nil
	}

	state, err := TaskState(tx, taskID)
	if err != nil {
		return err
	}

	// ready|in_progress|in_review → merged
	switch state {
	case "ready", "in_progress", "in_review":
		if err := Transition(tx, now, taskID, state, "merged", eventID); err != nil {
			return err
		}
		if err := CloseActiveLease(tx, now, taskID); err != nil {
			return err
		}
		state = "merged"
	case "merged", "deployed_dev":
		// continue below
	default:
		return nil // draft, abandoned, or already terminal
	}

	doneState, err := RepoDoneState(tx, repo)
	if err != nil {
		return err
	}

	covered := func(frontier *int64) bool {
		return frontier != nil && *frontier >= *landed
	}

	if state == "merged" {
		dev, err := ConfirmedFrontier(tx, repo, "dev")
		if err != nil {
			return err
		}
		if covered(dev) {
			if err := Transition(tx, now, taskID, "merged", "deployed_dev", eventID); err != nil {
				return err
			}
			state = "deployed_dev"
		}
	}

	prod, err := ConfirmedFrontier(tx, repo, "prod")
	if err != nil {
		return err
	}
	if covered(prod) && (state == "merged" || state == "deployed_dev") {
		return Transition(tx, now, taskID, state, "deployed_prod", eventID)
	}

	if doneState == "released" && (state == "merged" || state == "deployed_dev") {
		rel, err := ReleaseFrontier(tx, repo)
		if err != nil {
			return err
		}
		if covered(rel) {
			return Transition(tx, now, taskID, state, "released", eventID)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestResolveDelivery -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/delivery.go internal/store/delivery_test.go
git commit -m "Add delivery resolver: forward-only, order-independent task advancement"
```

---

### Task 7: Hooks — GitHub `push` handler

**Files:**
- Create: `internal/hooks/push.go`
- Create: `internal/hooks/push_test.go`
- Create: `internal/hooks/testdata/github/push_branch.json`, `push_main_merge.json`, `push_main_marker.json`, `push_last_deploy.json`
- Modify: `internal/hooks/github.go` (envelope + router)

- [ ] **Step 1: Create fixtures**

Follow the shape of real GitHub push payloads; the handler only needs these fields. Fixture repo is `sunstoneinstitute/demo`; task id `P1-1` (tests create it). Check how existing tests in `github_test.go` seed projects/tasks and reuse those helpers.

`push_branch.json`:

```json
{
  "ref": "refs/heads/lode/P1-1-add-widget",
  "repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"},
  "commits": [
    {"id": "1111111111111111111111111111111111111111", "message": "Add widget"},
    {"id": "2222222222222222222222222222222222222222", "message": "Widget tests"}
  ],
  "head_commit": {"id": "2222222222222222222222222222222222222222", "message": "Widget tests"}
}
```

`push_main_merge.json` (merge commit of the branch above, plus its commits):

```json
{
  "ref": "refs/heads/main",
  "repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"},
  "commits": [
    {"id": "1111111111111111111111111111111111111111", "message": "Add widget"},
    {"id": "2222222222222222222222222222222222222222", "message": "Widget tests"},
    {"id": "3333333333333333333333333333333333333333", "message": "Merge branch 'lode/P1-1-add-widget'"}
  ],
  "head_commit": {"id": "3333333333333333333333333333333333333333", "message": "Merge branch 'lode/P1-1-add-widget'"}
}
```

`push_main_marker.json` (direct commit, marker trailer):

```json
{
  "ref": "refs/heads/main",
  "repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"},
  "commits": [
    {"id": "4444444444444444444444444444444444444444", "message": "Hotfix crash on empty input\n\nWL-Task: P1-1"}
  ],
  "head_commit": {"id": "4444444444444444444444444444444444444444", "message": "Hotfix crash on empty input\n\nWL-Task: P1-1"}
}
```

`push_last_deploy.json` (cherry-pick with main-sha trailer):

```json
{
  "ref": "refs/heads/last-deploy/dev",
  "repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"},
  "commits": [
    {"id": "5555555555555555555555555555555555555555", "message": "Add widget\n\nmain-sha: 3333333333333333333333333333333333333333"}
  ],
  "head_commit": {"id": "5555555555555555555555555555555555555555", "message": "Add widget\n\nmain-sha: 3333333333333333333333333333333333333333"}
}
```

- [ ] **Step 2: Write failing tests**

`internal/hooks/push_test.go` — follow `github_test.go`'s harness exactly (it builds a signed request with `X-Hub-Signature-256`/`X-GitHub-Event`/`X-GitHub-Delivery` and posts to the handler; reuse its helper functions — grep for `signBody` / fixture-loading helpers and copy their call shape). Scenarios:

```go
// TestPushBranchRecordsTaskCommits: seed project P1 + repo mapping +
// task P1-1 in state in_progress. POST push_branch.json with event=push.
// Assert: task_commits has (P1-1, repo, 1111...) and (P1-1, repo, 2222...),
// task state unchanged (in_progress).

// TestPushMainMergeAdvancesTask: same seed; POST push_branch.json then
// push_main_merge.json. Assert: task state = merged; main_commits has 3 rows
// for the repo; task_commits includes the merge sha 3333... (source
// merge_message).

// TestPushMainMarkerAdvancesTask: seed task in state ready; POST
// push_main_marker.json only. Assert: state = merged; task_commits has
// 4444... with source marker.

// TestPushLastDeployMapsShas: POST push_main_merge.json (so 3333... exists
// in main_commits), then push_last_deploy.json. Assert: deploy_shas maps
// (repo, 5555...) to the main_commits id of 3333....

// TestPushUnmappedRepoIgnored: POST push_branch.json for a repo with no
// project mapping. Assert: response status "ignored", no task_commits rows.
```

Write these as real tests (the harness pattern from `github_test.go` gives you `*store.Store` access for direct SQL assertions — query via `s.DB()` if a DB accessor exists, otherwise add assertions through store functions inside a transaction).

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/hooks/ -run TestPush -count=1`
Expected: FAIL — handler routes `push` to nil apply (events recorded, no facts).

- [ ] **Step 4: Implement**

In `internal/hooks/github.go`:
- Add `DefaultBranch string \`json:"default_branch"\`` to the `envelope.Repository` struct.
- In `applyFunc`, add:

```go
	case "push":
		return func(tx *sql.Tx, eventID int64) error {
			return h.applyPush(tx, eventID, repo, env.Repository.DefaultBranch, body)
		}
```

Create `internal/hooks/push.go`:

```go
package hooks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// pushPayload is the part of a GitHub push event the handler needs.
type pushPayload struct {
	Ref     string `json:"ref"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"commits"`
}

// mergeMessagePatterns extract the merged branch name from merge-commit
// messages ("Merge branch 'x'", "Merge pull request #1 from owner/x").
var mergeMessagePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^Merge branch '([^']+)'`),
	regexp.MustCompile(`^Merge pull request #\d+ from [^/\s]+/(\S+)`),
}

// markerPattern matches a "WL-Task: <id>" line anywhere in a commit message.
var markerPattern = regexp.MustCompile(`(?m)^WL-Task:\s*([A-Z][A-Z0-9]*-[0-9]+)`)

// mainSHATrailer matches the main-sha trailer sunstoneinstitute's
// update-deploy-branch action stamps on last-deploy/* cherry-picks.
var mainSHATrailer = regexp.MustCompile(`(?mi)^main-sha:\s*([0-9a-f]{7,40})`)

// applyPush routes a push by ref: task-branch pushes attribute commits to
// the task, default-branch pushes append main_commits and advance landed
// tasks, last-deploy/* pushes map deploy shas back to main commits.
func (h *githubHandler) applyPush(tx *sql.Tx, eventID int64, repo, defaultBranch string, body []byte) error {
	var p pushPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse push payload: %w", err)
	}
	branch, ok := strings.CutPrefix(p.Ref, "refs/heads/")
	if !ok {
		return nil // tag pushes etc.
	}
	now := h.st.Now()

	if taskID := store.TaskIDFromRef(branch); taskID != "" {
		for _, c := range p.Commits {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: c.ID,
				Source: "branch_push", SeenAt: now,
			}); err != nil {
				// A branch named after a nonexistent task must not fail
				// the delivery (FK violation): log and skip the rest.
				h.log.Warn("push: task commit insert failed", "task", taskID, "err", err)
				return nil
			}
		}
		return nil
	}

	if defaultBranch != "" && branch == defaultBranch {
		affected := map[string]bool{}
		for _, c := range p.Commits {
			mainID, err := store.AppendMainCommit(tx, repo, c.ID, now)
			if err != nil {
				return err
			}
			_ = mainID
			// Attribute by message: merge-commit branch name or marker.
			for _, taskID := range taskIDsFromMessage(c.Message) {
				if err := store.InsertTaskCommit(tx, store.TaskCommit{
					TaskID: taskID, Repo: repo, SHA: c.ID,
					Source: sourceForMessage(c.Message), SeenAt: now,
				}); err != nil {
					h.log.Warn("push: message attribution failed", "task", taskID, "err", err)
					continue
				}
				affected[taskID] = true
			}
			// Attribute by prior branch-push tracking.
			ids, err := taskIDsForSHA(tx, repo, c.ID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				affected[id] = true
			}
		}
		for taskID := range affected {
			if err := store.ResolveDelivery(tx, now, taskID, repo, eventID); err != nil {
				return err
			}
		}
		return nil
	}

	if env, ok := strings.CutPrefix(branch, "last-deploy/"); ok {
		if store.NormalizeEnvironment(env) == "" {
			return nil
		}
		for _, c := range p.Commits {
			m := mainSHATrailer.FindStringSubmatch(c.Message)
			if m == nil {
				continue
			}
			mainID, err := store.MainIDForSHA(tx, repo, m[1])
			if err != nil {
				return err
			}
			if mainID == nil {
				continue // main push not seen (yet); harmless
			}
			if err := store.MapDeploySHA(tx, repo, c.ID, *mainID); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// taskIDsFromMessage extracts task ids from a commit message: the branch
// named in a merge-commit subject, plus any WL-Task marker line.
func taskIDsFromMessage(msg string) []string {
	var out []string
	for _, pat := range mergeMessagePatterns {
		if m := pat.FindStringSubmatch(msg); m != nil {
			if id := store.TaskIDFromRef(m[1]); id != "" {
				out = append(out, id)
			}
		}
	}
	if m := markerPattern.FindStringSubmatch(msg); m != nil {
		out = append(out, m[1])
	}
	return out
}

func sourceForMessage(msg string) string {
	if markerPattern.MatchString(msg) {
		return "marker"
	}
	return "merge_message"
}

// taskIDsForSHA returns tasks already attributed to sha in repo (from
// earlier branch pushes or PR correlation).
func taskIDsForSHA(tx *sql.Tx, repo, sha string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT task_id FROM task_commits WHERE repo = $1 AND sha = $2`,
		repo, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
```

Note the FK-failure handling: `InsertTaskCommit` for a task id that doesn't exist violates the `task_commits.task_id` FK and would poison the transaction. Either pre-check the task exists (`SELECT 1 FROM tasks WHERE id=$1`) before inserting — do that if the warn-and-return approach fails the tests — or accept failing the apply. Prefer the pre-check: add a small `taskExists(tx, id) (bool, error)` helper in push.go and skip unknown ids silently. Correlation must never fail the delivery (spec: Error handling).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/hooks/ -run TestPush -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/hooks/push.go internal/hooks/push_test.go internal/hooks/testdata/github/push_*.json internal/hooks/github.go
git commit -m "Handle GitHub push events: task commits, main ordering, deploy-sha mapping"
```

---

### Task 8: Hooks — rework `pull_request`, add `deployment_status`, extend `release`

**Files:**
- Modify: `internal/hooks/github.go`
- Create: `internal/hooks/deployment.go`
- Create: `internal/hooks/testdata/github/deployment_status_success.json`
- Modify: `internal/hooks/github_test.go`

- [ ] **Step 1: Fixture**

`deployment_status_success.json`:

```json
{
  "action": "created",
  "deployment_status": {"state": "success"},
  "deployment": {
    "environment": "dev",
    "sha": "3333333333333333333333333333333333333333"
  },
  "repository": {"full_name": "sunstoneinstitute/demo", "default_branch": "main"}
}
```

- [ ] **Step 2: Write failing tests** (in `github_test.go` or a new `deployment_test.go`)

```go
// TestDeploymentStatusAdvancesTask: seed task P1-1 in_progress; POST
// push_branch.json, push_main_merge.json (task now merged), then
// deployment_status_success.json (event=deployment_status). Assert:
// task state = deployed_dev; env_deploys row (repo, dev) has gh watermark.

// TestDeploymentStatusUnknownSHAIgnored: POST only
// deployment_status_success.json (no main push seen). Assert: 200 ok,
// no env_deploys row, no crash.

// TestPRMergeRecordsFactsAndResolves: existing merged-PR test — update it:
// merged PR now records head/merge shas into task_commits and the task
// only reaches merged once the corresponding push-to-main event arrives
// (or immediately if the push came first). Add both orderings:
//   PR-merged then push-main → merged after the push.
//   push-main then PR-merged → merged after the PR event.

// TestReleaseSetsFrontier: repo done_state=released; task merged via
// push; POST release_published.json (existing fixture — check its
// tag_name). Assert: task state = released; release_frontiers row exists.
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/hooks/ -run 'TestDeployment|TestPRMerge|TestRelease' -count=1`
Expected: FAIL.

- [ ] **Step 4: Implement**

In `internal/hooks/github.go` `applyFunc`, add:

```go
	case "deployment_status":
		return func(tx *sql.Tx, eventID int64) error {
			return h.applyDeploymentStatus(tx, eventID, repo, body)
		}
```

Create `internal/hooks/deployment.go`:

```go
package hooks

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// applyDeploymentStatus records a successful GitHub deployment as the
// gh-side watermark for the normalized environment, then advances every
// task covered by the new confirmed frontier.
func (h *githubHandler) applyDeploymentStatus(tx *sql.Tx, eventID int64, repo string, body []byte) error {
	var p struct {
		DeploymentStatus struct {
			State string `json:"state"`
		} `json:"deployment_status"`
		Deployment struct {
			Environment string `json:"environment"`
			SHA         string `json:"sha"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("parse deployment_status payload: %w", err)
	}
	if p.DeploymentStatus.State != "success" {
		return nil
	}
	env := store.NormalizeEnvironment(p.Deployment.Environment)
	if env == "" {
		return nil
	}
	mainID, err := store.MainIDForSHA(tx, repo, p.Deployment.SHA)
	if err != nil {
		return err
	}
	if mainID == nil {
		return nil // deploy of a sha we've never seen; nothing to anchor to
	}
	now := h.st.Now()
	if err := store.BumpEnvDeployGH(tx, now, repo, env, *mainID); err != nil {
		return err
	}
	return resolveFrontier(tx, now, repo, env, eventID)
}

// resolveFrontier re-reads the confirmed frontier for repo/env and resolves
// every task at or below it. Shared with the Flux handler (Task 9).
func resolveFrontier(tx *sql.Tx, now time.Time, repo, env string, eventID int64) error {
	frontier, err := store.ConfirmedFrontier(tx, repo, env)
	if err != nil {
		return err
	}
	if frontier == nil {
		return nil
	}
	tasks, err := store.TasksBelowFrontier(tx, repo, *frontier)
	if err != nil {
		return err
	}
	for _, taskID := range tasks {
		if err := store.ResolveDelivery(tx, now, taskID, repo, eventID); err != nil {
			return err
		}
	}
	return nil
}
```

(Add `"time"` to deployment.go's imports.)

In `applyPullRequest` (github.go), replace the post-upsert `switch` block with:

```go
	switch {
	case action == "opened" || action == "ready_for_review":
		taskState, err := store.TaskState(tx, taskID)
		if err != nil {
			return err
		}
		if taskState == "in_progress" {
			return store.Transition(tx, now, taskID, "in_progress", "in_review", eventID)
		}
	case action == "closed" && gh.Merged:
		if err := store.CloseActiveLease(tx, now, taskID); err != nil {
			return err
		}
		// Record the PR's shas as task commits; the resolver advances the
		// task once (and if) they appear on main via a push event.
		if gh.Head.SHA != "" {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: gh.Head.SHA, Source: "pr", SeenAt: now,
			}); err != nil {
				return err
			}
		}
		if gh.MergeCommitSHA != nil && *gh.MergeCommitSHA != "" {
			if err := store.InsertTaskCommit(tx, store.TaskCommit{
				TaskID: taskID, Repo: repo, SHA: *gh.MergeCommitSHA, Source: "pr", SeenAt: now,
			}); err != nil {
				return err
			}
		}
		return store.ResolveDelivery(tx, now, taskID, repo, eventID)
	}
	return nil
```

(The `project` parameter to `applyPullRequest` becomes unused — remove it from the signature and the call site.)

In `applyRelease`, after `CreateArtifact`, add (and change the signature to accept `eventID int64` — update `applyFunc` accordingly):

```go
	// Record the release frontier: releases tag main's head, so the newest
	// main commit we've seen is what the release covers.
	var latest sql.NullInt64
	if err := tx.QueryRow(`SELECT max(id) FROM main_commits WHERE repo = $1`, repo).Scan(&latest); err != nil {
		return fmt.Errorf("latest main commit for %s: %w", repo, err)
	}
	if !latest.Valid {
		return nil
	}
	if err := store.SetReleaseFrontier(tx, repo, p.Release.TagName, latest.Int64, publishedAt); err != nil {
		return err
	}
	tasks, err := store.TasksBelowFrontier(tx, repo, latest.Int64)
	if err != nil {
		return err
	}
	for _, taskID := range tasks {
		if err := store.ResolveDelivery(tx, h.st.Now(), taskID, repo, eventID); err != nil {
			return err
		}
	}
	return nil
```

where `publishedAt` falls back to `h.st.Now()` if `p.Release.PublishedAt.IsZero()`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/hooks/ -count=1`
Expected: PASS (including updated legacy PR tests).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "Wire pull_request, deployment_status, and release into the delivery resolver"
```

---

### Task 9: Hooks — Flux confirmation

**Files:**
- Modify: `internal/hooks/flux.go`
- Modify: `internal/hooks/flux_test.go` (+ a fixture variant if needed)

- [ ] **Step 1: Write failing test**

The existing `kustomization_succeeded.json` fixture carries `metadata.revision` — check its SHA value; if it's not a full hex SHA, add a fixture copy `kustomization_succeeded_delivery.json` with `"revision": "main@sha1:3333333333333333333333333333333333333333"` and `"cluster"` metadata matching the handler's clusterEnv map in tests.

```go
// TestFluxSuccessConfirmsFrontier: seed task P1-1, push_branch +
// push_main_merge via the GitHub handler (task merged), then
// deployment_status_success (gh watermark set, task deployed_dev — with
// flux_seen still false the gh signal alone confirmed). Now POST the flux
// success event whose revision sha is 3333... to the flux handler with
// clusterEnv {"dev-cluster": "dev"}. Assert: env_deploys.flux_seen = true,
// flux watermark set, task still deployed_dev (idempotent).

// TestFluxGatesAfterFirstSeen: fresh task/repo. Send flux success FIRST
// (flux_seen latches, flux watermark set), then main push, then
// deployment_status success for a NEWER main commit than the flux
// watermark. Assert: task advances only up to the flux watermark
// (min(gh, flux) — i.e. if flux confirmed m1 and gh confirmed m2, a task
// landed at m2 stays merged).
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/hooks/ -run TestFlux -count=1`
Expected: new tests FAIL (flux handler records deployments but never touches env_deploys).

- [ ] **Step 3: Implement**

In `flux.go` `apply`, in the `ev.Reason == "ReconciliationSucceeded"` branch, after the existing `UpsertDeployment`/recovery logic, add:

```go
	// Delivery confirmation: if the revision sha maps to a repo we track,
	// advance the flux watermark for this environment and resolve tasks.
	if sha := revisionSHA(ev.Metadata["revision"]); sha != "" {
		repo, mainID, err := store.MainIDForSHAAnyRepo(tx, sha)
		if err != nil {
			return err
		}
		if mainID != nil {
			if err := store.BumpEnvDeployFlux(tx, now, repo, environment, *mainID); err != nil {
				return err
			}
			if err := resolveFrontier(tx, now, repo, environment, 0); err != nil {
				return err
			}
		}
	}
```

Notes: `environment` here is already the cluster-resolved env ("dev"/"prod" — `resolveEnvironment` returns exactly those). `resolveFrontier` is the helper from Task 8 (same package). The eventID passed is 0 — flux.go's apply closure doesn't receive eventID today; change the closure `apply = func(tx *sql.Tx, _ int64) error` to capture it (`func(tx *sql.Tx, eventID int64) error { return h.apply(tx, eventID, ev) }`) and thread it through instead of 0.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/hooks/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/flux.go internal/hooks/flux_test.go internal/hooks/testdata/flux/
git commit -m "Confirm env deploys from Flux reconciliation events"
```

---

### Task 10: done_state — storage, admin API, CLI

**Files:**
- Modify: `internal/store/projects.go` (Repo listing + SetRepoDoneState)
- Modify: `internal/api/admin.go` (+ route in `internal/api/server.go`)
- Modify: `internal/cmd/project.go`, `internal/cli` client
- Test: `internal/store/projects_test.go`, `internal/api/admin_test.go` (or wherever addRepo is tested), `internal/cmd/project_test.go` if present

- [ ] **Step 1: Write failing store test** (in `projects_test.go`)

```go
func TestSetRepoDoneState(t *testing.T) {
	// seed project + AddRepo("p1", "acme/app") using existing helpers
	// SetRepoDoneState(ctx, "acme/app", "released") → nil
	// read back done_state = 'released'
	// SetRepoDoneState(ctx, "acme/app", "bogus") → ErrInvalidInput
	// SetRepoDoneState(ctx, "acme/nope", "released") → ErrNotFound
}
```

Write it out fully following the file's existing test style.

- [ ] **Step 2: Implement store method** (`projects.go`)

```go
// validDoneStates are the terminal states a repo mapping may declare as
// "fully delivered" (docs/specs/004-execution-backbone.md).
var validDoneStates = map[string]bool{"merged": true, "deployed_prod": true, "released": true}

// SetRepoDoneState sets the delivery terminal state for a mapped repo.
func (s *Store) SetRepoDoneState(ctx context.Context, repo, state string) error {
	if !validDoneStates[state] {
		return fmt.Errorf("done_state %q: %w", state, ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE project_repos SET done_state = $1 WHERE repo = $2`, state, repo)
	if err != nil {
		return fmt.Errorf("set done_state for %s: %w", repo, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("repo %s: %w", repo, ErrNotFound)
	}
	return nil
}
```

Also extend whatever query feeds the project-list/response with repos so it selects `done_state` too (grep `FROM project_repos` in projects.go; add a `RepoMapping {Repo, DoneState string}` type if the current code returns bare strings, and adjust `internal/api/admin.go`'s project JSON to include it — `"repos": [{"repo": ..., "done_state": ...}]` **breaks the CLI's project list rendering; check `internal/cmd/project.go` and `internal/cli` for the response struct and update both sides together.**)

- [ ] **Step 3: Admin API**

- Extend the addRepo request struct in `admin.go` with `DoneState string \`json:"done_state"\`` (optional; validate against merged/deployed_prod/released when non-empty; call `SetRepoDoneState` after `AddRepo`).
- New handler + route:

```go
mux.Handle("PATCH /api/v1/repos/{owner}/{name}", s.auth(requireAdmin(s.patchRepo)))
```

```go
// patchRepo handles PATCH /api/v1/repos/{owner}/{name}: currently only
// done_state is settable.
func (s *server) patchRepo(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("owner") + "/" + r.PathValue("name")
	var req struct {
		DoneState string `json:"done_state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.DoneState == "" {
		writeErr(w, http.StatusUnprocessableEntity, "done_state is required")
		return
	}
	if err := s.st.SetRepoDoneState(r.Context(), repo, req.DoneState); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

(Match error-mapping/auth helpers to the file's existing handlers.)

- [ ] **Step 4: CLI**

- `lode project add-repo` gains `--done-state` (empty = server default/discovery).
- New subcommand `lode project set-repo <owner/name> --done-state <state>` calling the PATCH endpoint. Follow `newProjectAddRepoCmd` (project.go:85) as the template; add the client method next to `AddRepo` in `internal/cli`.

- [ ] **Step 5: Run tests, commit**

Run: `go build ./... && go test ./internal/store/ ./internal/api/ ./internal/cmd/ -count=1`
Expected: PASS.

```bash
git add -A
git commit -m "Add per-repo done_state with admin API and CLI support"
```

---

### Task 11: GitHub App environment discovery (seeds done_state)

**Files:**
- Create: `internal/githubauth/app.go`
- Create: `internal/githubauth/app_test.go`
- Modify: `internal/api/server.go` (Config), `internal/cmd/serve.go`, `internal/api/admin.go` (addRepo discovery hook)

- [ ] **Step 1: Write failing test** (JWT + discovery against `httptest.Server`)

```go
// TestAppJWTAndDiscovery: generate an RSA key in-test
// (rsa.GenerateKey(rand.Reader, 2048)). Spin an httptest.Server that:
//   GET /repos/acme/app/installation           → {"id": 42}
//   POST /app/installations/42/access_tokens   → {"token": "ghs_test"}
//   GET /repos/acme/app/environments           → {"environments":[{"name":"dev"},{"name":"prod"},{"name":"copilot"}]}
//   GET /repos/acme/app/releases/latest        → 404
// Construct AppAuth{AppID: "12345", Key: key, BaseURL: server.URL}.
// DiscoverDoneState(ctx, "acme/app") → "deployed_prod".
// Second variant: environments empty + releases/latest → 200 {"tag_name":"v1"}
// → "released". Third: environments empty + 404 releases → "merged".
// Assert the requests to /app/... carried "Bearer <jwt>" and the /repos
// data requests carried "token ghs_test" (or Bearer — match implementation).
```

- [ ] **Step 2: Implement `internal/githubauth/app.go`**

Use `github.com/go-jose/go-jose/v4` (already a dependency) for the RS256 app JWT:

```go
// GitHub App (installation) authentication and repo delivery-profile
// discovery. Optional: when the app id/key are not configured, discovery is
// skipped and done_state stays at its default.
package githubauth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// AppAuth signs GitHub App JWTs and mints installation tokens.
type AppAuth struct {
	AppID   string
	Key     *rsa.PrivateKey
	BaseURL string // https://api.github.com, overridable in tests
	Client  *http.Client
}

// ParseAppPrivateKey parses the PEM private key GitHub issues for an App.
func ParseAppPrivateKey(pemData []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("github app key: no PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app key: not RSA")
	}
	return key, nil
}

func (a *AppAuth) appJWT() (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: a.Key}, nil)
	if err != nil {
		return "", err
	}
	now := time.Now()
	return jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   a.AppID,
		IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
		Expiry:   jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}).Serialize()
}

func (a *AppAuth) get(ctx context.Context, url, auth string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, nil
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// InstallationToken mints a short-lived installation token scoped to the
// installation that owns repo ("owner/name").
func (a *AppAuth) InstallationToken(ctx context.Context, repo string) (string, error) {
	jwtStr, err := a.appJWT()
	if err != nil {
		return "", err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	code, err := a.get(ctx, a.BaseURL+"/repos/"+repo+"/installation", "Bearer "+jwtStr, &inst)
	if err != nil || code >= 300 {
		return "", fmt.Errorf("installation lookup for %s: code=%d err=%w", repo, code, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/app/installations/%d/access_tokens", a.BaseURL, inst.ID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.Token == "" {
		return "", fmt.Errorf("empty installation token for %s (status %d)", repo, resp.StatusCode)
	}
	return tok.Token, nil
}

// DiscoverDoneState inspects a repo's GitHub environments and releases and
// returns the done_state they imply: a prod-ish environment → deployed_prod;
// releases without one → released; neither → merged.
func (a *AppAuth) DiscoverDoneState(ctx context.Context, repo string) (string, error) {
	token, err := a.InstallationToken(ctx, repo)
	if err != nil {
		return "", err
	}
	auth := "Bearer " + token
	var envs struct {
		Environments []struct {
			Name string `json:"name"`
		} `json:"environments"`
	}
	if code, err := a.get(ctx, a.BaseURL+"/repos/"+repo+"/environments", auth, &envs); err != nil || code >= 300 {
		return "", fmt.Errorf("list environments for %s: code=%d err=%w", repo, code, err)
	}
	for _, e := range envs.Environments {
		if normalizeEnv(e.Name) == "prod" {
			return "deployed_prod", nil
		}
	}
	code, err := a.get(ctx, a.BaseURL+"/repos/"+repo+"/releases/latest", auth, nil)
	if err != nil {
		return "", err
	}
	if code == http.StatusOK {
		return "released", nil
	}
	return "merged", nil
}

// normalizeEnv mirrors store.NormalizeEnvironment without importing store
// (githubauth stays dependency-light).
func normalizeEnv(name string) string {
	switch strings.ToLower(name) {
	case "dev", "test", "development", "staging":
		return "dev"
	case "prod", "production":
		return "prod"
	default:
		return ""
	}
}
```

(Add `"strings"` import. If go-jose's `jwt` sub-package isn't already vendored via go-oidc, `go get github.com/go-jose/go-jose/v4/jwt` — it's the same module, no new dependency.)

- [ ] **Step 3: Wire into serve + addRepo**

- `Config` gains `GitHubAppID string` and `GitHubAppPrivateKey string` (PEM). `serve.go`: `GitHubAppID: os.Getenv("LODE_GITHUB_APP_ID"), GitHubAppPrivateKey: os.Getenv("LODE_GITHUB_APP_PRIVATE_KEY")`.
- In server construction: if both set, parse the key and store `appAuth *githubauth.AppAuth` on the server (BaseURL `https://api.github.com`); else nil.
- In `addRepo` (admin.go), after a successful `AddRepo` and only when the request carried no explicit `done_state` and `s.appAuth != nil`: call `DiscoverDoneState` with a 5-second timeout context; on success `SetRepoDoneState`; on error log at Warn and continue (discovery never fails the request — spec). Include the discovered value in the response JSON as `"done_state"`.

- [ ] **Step 4: Run tests, commit**

Run: `go build ./... && go test ./internal/githubauth/ ./internal/api/ -count=1`
Expected: PASS.

```bash
git add -A
git commit -m "Discover repo done_state via GitHub App environments API"
```

---

### Task 12: Timeline delivery entries + surface polish

**Files:**
- Modify: `internal/api/timeline.go`, `internal/store/delivery.go`
- Test: `internal/api/timeline_test.go`

- [ ] **Step 1: Write failing test** (extend `timeline_test.go` — follow its existing seeding style)

```go
// TestTimelineDeliveryEntries: seed task with a task_commit + main_commit
// (landed), an env_deploys dev row covering it, and a release_frontiers row.
// GET timeline. Assert entries with "type":"landed" (repo, sha),
// "type":"deployed" (environment dev), "type":"released" (tag).
```

- [ ] **Step 2: Implement**

Store helper in `delivery.go`:

```go
// DeliveryFacts summarizes a task's delivery progress for the timeline.
type DeliveryFacts struct {
	Repo       string
	LandedSHA  string
	LandedAt   time.Time
	Deployed   []DeployFact   // confirmed envs covering the landed commit
	ReleaseTag string         // "" if not released
	ReleasedAt time.Time
}

type DeployFact struct {
	Environment string
	At          time.Time
}

// DeliveryFactsForTask returns per-repo delivery facts for a task, or nil
// if its work has not landed. (Query task_commits joined to main_commits
// for the landed sha/pushed_at; env_deploys rows whose ConfirmedFrontier
// covers the landed id; the earliest release_frontiers row covering it.)
func (s *Store) DeliveryFactsForTask(ctx context.Context, taskID string) ([]DeliveryFacts, error) {
	// Implement with the same read-tx pattern the file's other
	// context-taking helpers use (see CIRunsForSHA in changes.go for the
	// query/scan shape); one query per fact table is fine.
}
```

Write the three queries concretely (landed: the Task 4 `LandedMainID` join extended with `mc.sha, mc.pushed_at`; deployed: `SELECT environment, updated_at FROM env_deploys WHERE repo=$1` filtered through `ConfirmedFrontier` semantics — replicate the min/flux_seen logic in SQL or call the helpers inside a tx; released: `SELECT tag, published_at FROM release_frontiers WHERE repo=$1 AND main_id >= $2 ORDER BY main_id LIMIT 1`).

API: in `taskTimeline` add `deliveryEntries` alongside `prEntries`/`ciEntries`:

```go
func (s *server) deliveryEntries(ctx context.Context, taskID string) ([]timelineEntry, error) {
	facts, err := s.st.DeliveryFactsForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var out []timelineEntry
	for _, f := range facts {
		out = append(out, timelineEntry{at: f.LandedAt, obj: map[string]any{
			"at": f.LandedAt, "type": "landed", "repo": f.Repo, "sha": f.LandedSHA,
		}})
		for _, d := range f.Deployed {
			out = append(out, timelineEntry{at: d.At, obj: map[string]any{
				"at": d.At, "type": "deployed", "repo": f.Repo, "environment": d.Environment,
			}})
		}
		if f.ReleaseTag != "" {
			out = append(out, timelineEntry{at: f.ReleasedAt, obj: map[string]any{
				"at": f.ReleasedAt, "type": "released", "repo": f.Repo, "tag": f.ReleaseTag,
			}})
		}
	}
	return out, nil
}
```

- [ ] **Step 3: Run tests, commit**

Run: `go test ./internal/api/ ./internal/store/ -count=1`
Expected: PASS.

```bash
git add -A
git commit -m "Show delivery facts in the task timeline"
```

---

### Task 13: Docs and deploy config

**Files:**
- Modify: `docs/spec.md` (state machine + webhook handler lists)
- Modify: `README.md` (if it names task states — grep `in_review\|done`)
- Modify: `deploy/overlays/hzdev/externalsecret-worklode-secrets.yaml`, `deploy/overlays/hzprod/externalsecret-worklode-secrets.yaml` (add `LODE_GITHUB_APP_ID`, `LODE_GITHUB_APP_PRIVATE_KEY`)

- [ ] **Step 1: Update `docs/spec.md`**

- Line ~64 state machine: `draft → ready → in_progress → in_review → merged → deployed_dev → deployed_prod` plus `released` (release-based repos) and `abandoned`; point to `docs/specs/004-execution-backbone.md` for delivery semantics.
- Line ~68 `pull_requests` correlation: branch prefix now configurable (`LODE_BRANCH_PREFIX`, default `lode/`).
- Line ~78 GitHub webhook handler list: add `push → task_commits/main_commits/deploy_shas; deployment_status → env_deploys`; replace the deploy-gating sentence with the resolver summary (facts + `ResolveDelivery`, done_state per repo).

Keep edits tight — the design doc is the authority; spec.md just needs to stop contradicting it.

- [ ] **Step 2: Deploy overlays**

Add to both externalsecret files, following the existing entry shape (same 1Password item, new properties):

```yaml
    - secretKey: LODE_GITHUB_APP_ID
      remoteRef:
        key: <same key as the existing LODE_GITHUB_APP_* entries>
        property: LODE_GITHUB_APP_ID
    - secretKey: LODE_GITHUB_APP_PRIVATE_KEY
      remoteRef:
        key: <same key>
        property: LODE_GITHUB_APP_PRIVATE_KEY
```

(Read the file first; copy the exact `remoteRef.key` used by `LODE_GITHUB_APP_CLIENT_ID`.) Note in the commit message that the 1Password item needs the two new fields and the GitHub App needs Actions:read + Deployments:read permissions and push/deployment_status event subscriptions — ops follow-up, not code.

- [ ] **Step 3: Commit**

```bash
git add docs/spec.md README.md deploy/overlays/
git commit -m "Update spec.md and deploy config for delivery lifecycle"
```

---

### Task 14: End-to-end flow test + full suite

**Files:**
- Create: `internal/hooks/delivery_flow_test.go`

- [ ] **Step 1: Write the walk-through test**

One test driving both handlers through the whole story (reuse the Task 7/8/9 fixtures):

```go
// TestDeliveryEndToEnd:
// 1. Seed project P1 (repo sunstoneinstitute/demo, done_state deployed_prod),
//    task P1-1, claim it (state in_progress).
// 2. push_branch.json            → task_commits recorded, still in_progress.
// 3. pull_request opened (existing fixture, head ref lode/P1-1-add-widget)
//                                → in_review.
// 4. push_main_merge.json        → merged (lease closed).
// 5. push_last_deploy.json       → deploy_shas mapped.
// 6. deployment_status_success   → deployed_dev (gh-only bootstrap).
// 7. flux success (revision 3333...) via flux handler, cluster→prod map
//    {"prod-cluster":"prod"}, gh prod watermark via a second
//    deployment_status fixture with environment "prod" and sha 5555...
//    (add fixture deployment_status_prod.json) → deployed_prod.
// Assert state after each step.
```

Order the prod-leg carefully: send the prod `deployment_status` (gh watermark; flux not yet seen for prod → advances immediately per bootstrap rule). Then send the flux prod success and assert the state is unchanged and `flux_seen` is now true — documenting the bootstrap-then-latch behavior end to end.

- [ ] **Step 2: Run everything**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS across all packages (e2e/ may need `docker compose up -d` — check `e2e/README` or its test file headers).

- [ ] **Step 3: Commit**

```bash
git add internal/hooks/delivery_flow_test.go
git commit -m "Add end-to-end delivery lifecycle test"
```

---

## Self-review notes (already applied)

- Spec coverage: state rename (T1/T2), branch prefix (T3), fact tables (T4/T5), resolver (T6), push (T7), PR/deployment_status/release (T8), Flux + bootstrap fallback (T9), done_state + admin/CLI (T10), discovery + App permissions (T11), timeline (T12), docs/deploy (T13), arrival-order + e2e tests (T6/T14). Multi-artifact repos and multi-repo tasks: deferred by spec — no tasks, correct.
- Flux `deployments` table and runtime events behavior is untouched (spec: "Existing deployments-table behavior unchanged").
- `lode task done` still transitions in_review → merged only; direct-to-main from other states is resolver-only. Manual completion of a never-landed task stays possible via `done` from in_review (unchanged semantics).
