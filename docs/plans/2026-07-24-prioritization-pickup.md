# Prioritization & Pickup (spec 02) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `docs/specs/worklode/02-prioritization-and-pickup.md`: `concern`, `project.focus`, `needs_decomposition`, the ranking function, and atomic `lode task claim --next`.

**Architecture:** Ranking is computed server-side in Go, not SQL: one query fetches the candidate ready set (ready + unblocked + unclaimed + not needs-decomposition), one recursive-CTE query computes `blocking_fan_out` for all candidates, Go sorts by the spec key, then attempts the spec-01 atomic `Claim` on each candidate in rank order — a lost race (`ErrLeased`/`ErrBlocked`/`ErrBadTransition`) just advances to the next candidate. No list→pick→claim window exists for callers; atomicity lives entirely in `Claim`.

**Tech Stack:** As plan 01 (Go, Postgres/pgx). Builds directly on the plan-01 codebase — do not start this plan until plan 01 is merged.

**Settled decisions:**
- `projects.focus` is `jsonb` (ordered array of concern strings), default `'[]'`.
- `needs_decomposition` is a `boolean NOT NULL DEFAULT false` column on `tasks` (the spec's "label").
- Ranking in Go over a bounded candidate fetch (the ready set is small; correctness comes from `Claim`, not from ranking inside one transaction). `--dry-run` returns the top-ranked candidate without claiming.
- Tiebreak: `created_at` asc, then numeric task id (`WL-9` < `WL-10` — parse the suffix; string compare is wrong).
- Priority rank for sorting: `critical=0, high=1, medium=2, low=3`.
- Setting `needs_decomposition` and `concern` post-creation goes through `PATCH /api/v1/tasks/{id}` + a new `lode task edit` command.
- The token budget behind the decomposition call (~100k) is documentation for reviewers, not code — no enforcement in this plan.

---

### Task 1: Migration 0002 — concern, needs_decomposition, focus

**Files:**
- Create: `deploy/base/migrations/0002_prioritization.up.sql`, `0002_prioritization.down.sql`

**Steps:**

- [ ] **Step 1: Write the migration** (load `golang-migrate:authoring`):

```sql
-- 0002_prioritization.up.sql
ALTER TABLE tasks ADD COLUMN concern text
    CHECK (concern IN ('completeness','performance','usability','security'));
ALTER TABLE tasks ADD COLUMN needs_decomposition boolean NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN focus jsonb NOT NULL DEFAULT '[]';
```

```sql
-- 0002_prioritization.down.sql
ALTER TABLE tasks DROP COLUMN concern;
ALTER TABLE tasks DROP COLUMN needs_decomposition;
ALTER TABLE projects DROP COLUMN focus;
```

- [ ] **Step 2:** `go test ./internal/store/ -run TestMigrateRoundTrip` green. Commit.

### Task 2: Store model — Task.Concern / NeedsDecomposition, Project.Focus

**Files:**
- Modify: `internal/store/tasks.go`, `tasks_test.go`
- Modify: `internal/store/projects.go`, `projects_test.go`

**Steps:**

- [ ] **Step 1: Tasks.** Add `Concern string` (empty = null) and `NeedsDecomposition bool` to the `Task` struct; extend the column list, scanner, `CreateTask` (new optional params via the existing pattern — follow how `priority`/`kind` flow today), and `UpdateTask`/patch path to accept `concern` (validated against the enum, `""`/`"none"` clears to NULL) and `needs_decomposition`. Write `concern` as `sql.NullString`.
- [ ] **Step 2: Projects.** Add `Focus []string` to `Project`; scan/write as jsonb (`json.Marshal`/`Unmarshal`). Add `SetProjectFocus(ctx, projectID string, focus []string) error` validating every entry against the concern enum (shared `ValidConcern(s string) bool` helper in `tasks.go`), recorded as a `cli` event `project.focus_set` via `RecordEvent` like other mutations.
- [ ] **Step 3: Tests.** Create task with concern; invalid concern rejected (`ErrInvalidInput`); focus set/get round-trips ordered; invalid focus entry rejected. `go test ./internal/store/` green. Commit.

### Task 3: Store — blocking fan-out

**Files:**
- Modify: `internal/store/tasks.go` (or new `internal/store/ranking.go`), test in `ranking_test.go`

**Steps:**

- [ ] **Step 1: Implement** `BlockingFanOut(ctx) (map[string]int, error)` — transitive count of tasks each task unblocks over open `blocks` edges:

```sql
WITH RECURSIVE closure(root, task) AS (
    SELECT from_task, to_task FROM task_edges WHERE type = 'blocks'
  UNION
    SELECT c.root, e.to_task
    FROM closure c JOIN task_edges e ON e.from_task = c.task AND e.type = 'blocks'
)
SELECT root, COUNT(DISTINCT task) FROM closure GROUP BY root
```

Tasks absent from the map have fan-out 0. (Unit-weight, all edges — matches spec D12; no filtering by blocked-task state for v1.)
- [ ] **Step 2: Test:** chain A blocks B blocks C, plus A blocks D → fanout(A)=3, fanout(B)=1, fanout(C)=0. Diamond (A blocks B, A blocks C, B blocks D, C blocks D) → fanout(A)=3 (D counted once). Green, commit.

### Task 4: Store — `ClaimNext`

**Files:**
- Create: `internal/store/ranking.go`, `internal/store/ranking_test.go`

**Steps:**

- [ ] **Step 1: Candidate fetch.** `readyCandidates(ctx, projectID string) ([]Task, error)`:

```sql
SELECT <task columns> FROM tasks t
WHERE t.state = 'ready'
  AND NOT t.needs_decomposition
  AND ($1 = '' OR t.project_id = $1)
  AND NOT EXISTS (SELECT 1 FROM leases l
                  WHERE l.task_id = t.id AND l.released_at IS NULL)
  AND NOT EXISTS (SELECT 1 FROM task_edges e
                  JOIN tasks b ON b.id = e.from_task
                  WHERE e.to_task = t.id AND e.type = 'blocks'
                    AND b.state NOT IN ('done','abandoned'))
```

- [ ] **Step 2: Rank.** Pure function, unit-testable without DB:

```go
type rankInput struct {
	Task    Task
	Focus   []string // the task's project focus
	FanOut  int
}

// rankTasks orders candidates by the spec-02 key:
// default:      (is_critical desc, concern_rank asc, priority asc, fan_out desc)
// strict-focus: (concern_rank asc, priority asc, fan_out desc)
// tiebreak: created_at asc, then numeric id asc.
func rankTasks(in []rankInput, strictFocus bool) []Task
```

`concernRank(concern string, focus []string) int`: index in focus; not listed or empty concern → `math.MaxInt`. `priorityRank`: critical 0 … low 3. Numeric id: `strconv.Atoi(strings.TrimPrefix(id, "WL-"))`.
- [ ] **Step 3: ClaimNext.**

```go
type ClaimNextOpts struct {
	ProjectID   string
	StrictFocus bool
	DryRun      bool
	Worktree    string
	ActorID     string
	TTL         time.Duration
}

type ClaimNextResult struct {
	Claimed bool
	Task    *Task   // set when Claimed or DryRun hit
	FanOut  int
	Lease   *Lease  // nil on DryRun
}

// ClaimNext ranks the ready set and atomically claims the top candidate,
// falling through to the next on a lost race. Empty ready set is not an
// error: returns Claimed=false, Task=nil.
func (s *Store) ClaimNext(ctx context.Context, opts ClaimNextOpts) (*ClaimNextResult, error)
```

Flow: fetch candidates → fetch fan-out map → fetch each involved project's focus (one query, `WHERE id = ANY($1)`) → `rankTasks` → if DryRun return top (no claim) → else loop candidates: `s.Claim(ctx, id, opts.ActorID, opts.Worktree, opts.TTL)`; on `ErrLeased`/`ErrBlocked`/`ErrBadTransition` continue; other error return; success → result. Exhausted → `Claimed:false`.
- [ ] **Step 4: Unit tests for `rankTasks`** — encode the spec's worked example as a table test (no DB):

Focus `[security, completeness]`; T1 high/completeness/5, T2 high/security/1, T3 critical/usability/0, T4 medium/security/8, T5 high/performance/12. Assert default order `[T3,T2,T4,T1,T5]` and strict order `[T2,T4,T1,T3,T5]`.
- [ ] **Step 5: DB tests for `ClaimNext`:** (a) worked-example fixture (create the 5 tasks; fan-out via real `blocks` edges to filler draft tasks: T1→5, T2→1, T4→8, T5→12) — default claims T3, strict (fresh fixture) claims T2; (b) soft focus: only off-focus tasks ready → still claims one; (c) `needs_decomposition` task never returned even when it's the only ready task (returns `Claimed:false`); (d) dry-run claims nothing (lease table empty after). Green, commit.

### Task 5: `ClaimNext` concurrency test

**Files:**
- Create: test in `internal/store/ranking_race_test.go`

**Steps:**

- [ ] **Step 1:** Spec acceptance 1: fixture with M=4 ready tasks, fire N=8 concurrent `ClaimNext` calls (distinct worktrees `h:/wt/<i>`, same actor) → exactly 4 return `Claimed:true` with 4 **distinct** task ids, 4 return `Claimed:false`; zero errors. `-race -count=3` green. Commit.

### Task 6: API — `POST /api/v1/tasks/claim-next` + PATCH extensions

**Files:**
- Modify: `internal/api/lifecycle.go` (+ tests), `internal/api/server.go` (route), task create/patch handlers & types

**Steps:**

- [ ] **Step 1: Route + handler.** `POST /api/v1/tasks/claim-next`, bearer-authed like claim. Request:

```json
{"project": "", "strict_focus": false, "dry_run": false, "worktree": "h:/path", "ttl_seconds": 0}
```

`worktree` required unless `dry_run`. Response (spec 02 shape):

```json
{"claimed": true, "task": {"id": "WL-7", "slug": "fix-the-thing", "concern": "security",
 "priority": "high", "fan_out": 3, "project": "worklode",
 "lease": {"worktree": "h:/path", "expires_at": "..."}}}
```

or `{"claimed": false, "reason": "no-ready-task"}` (HTTP 200 — an empty ready set is normal). `slug` = existing `SlugifyTitle(title)`.
- [ ] **Step 2: Create/patch.** `POST /tasks` accepts `concern`; `PATCH /tasks/{id}` accepts `concern` (empty string clears) and `needs_decomposition`. Project focus: `PATCH /api/v1/projects/{id}` (add if missing) accepting `{"focus": ["security","completeness"]}` (admin-token, consistent with other project mutations).
- [ ] **Step 3: Handler tests:** claim-next claims and returns spec JSON; none-ready returns 200 + reason; missing worktree → 400; PATCH invalid concern → 400. Green, commit.

### Task 7: CLI — `claim --next`, `task edit`, `project focus`

**Files:**
- Modify: `internal/cmd/task.go`, `internal/cmd/project.go`, `internal/cli/client.go` (+ tests)

**Steps:**

- [ ] **Step 1: `lode task claim [<id>] --next --project <p> --strict-focus --dry-run`.** `--next` and a positional id are mutually exclusive (error if both). `--next` posts to claim-next with worktree from `WorktreeIdentity(".")` (plan 01); `--json` prints the server response verbatim; human output prints `claimed WL-7 (fix-the-thing) — branch wl/WL-7-fix-the-thing` or `no ready task`. Exit 0 in both claimed and none-ready cases; non-zero only on real errors (spec acceptance 6).
- [ ] **Step 2: `lode task add --concern <c>`** and new **`lode task edit <id> [--concern <c|none>] [--priority <p>] [--needs-decomposition=<bool>]`** hitting PATCH.
- [ ] **Step 3: `lode project focus <id> [<concern> ...]`** — no concerns prints current focus; with args sets the ordered list; `--clear` empties. 
- [ ] **Step 4: CLI tests** following existing `internal/cmd`/`internal/cli` test patterns (httptest server). Include: `--next --json` none-ready prints `{"claimed":false,...}` and exits 0. Green, commit.

### Task 8: End-to-end pickup loop test

**Files:**
- Modify: `e2e/smoke_test.go` (or a new `e2e/pickup_test.go`, same build tag)

**Steps:**

- [ ] **Step 1:** Through public surfaces only: create project (set focus), create tasks with concerns/priorities/edges, `claim --next` via the API client → assert the spec-ordered task arrives, task is `in_progress`, lease worktree recorded; second claim-next gets the next task; `dry_run` leaves no lease. Green with `-tags e2e`. Commit.

---

## Acceptance criteria mapping (spec 02)

1. No-collision under contention → Task 5. 2. Worked example (T3 default / T2 strict, full order) → Task 4 steps 4–5. 3. Soft focus never idles → Task 4 step 5(b). 4. Critical bypass + `--strict-focus` opt-out → Task 4 (orders differ exactly by `is_critical`). 5. Decomposition gate → Task 4 step 5(c) (+ children claimable = normal ready tasks, covered by fixture). 6. Stable `--json`, none-ready exit 0 → Tasks 6–7. 7. Determinism/stable tiebreak → `rankTasks` unit tests (add a repeated-run assertion in Task 4 step 4).
