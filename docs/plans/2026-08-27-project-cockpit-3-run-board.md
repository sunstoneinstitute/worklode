---
status: draft
covers:
  - spec: docs/specs/032-project-cockpit.md#sec-8
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-13
    coverage: none
---
# Project cockpit part 3 — the run board (032 §8, visibility slice)

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** the one 032 §8 obligation whose facts already exist becomes a real
page: `GET /projects/{id}/work` groups the project's work into §8's six
groups — Ready, Running, Waiting, Needs judgment, Failed, and Completed —
and each active item shows its owner, delegate, lease age, last durable
event, cost, and PR/check state, from the fact readers the backbone already
has. The local-nav **Work** item stops being an `#work` anchor hack and
becomes the destination 032 §2 lists.

**Architecture:** a read-only projection, no new state. One pure classifier
(`runGroupOf`) and one pure assembler (`assembleRunBoard`) in
`internal/api/runboard.go` turn `store.ProjectWorkFact` rows plus session,
PR/CI, and cost maps into a `ui.RunBoardView`; two new bulk store readers
fill the fact families the existing bulk readers miss (open PRs per project,
costs per task set); a `runboard.templ` component renders it inside the
project sidebar frame. No migration, no event, no `/api/v1` route, no CLI
verb.

**Read first:**
- `docs/specs/inlined/032-project-cockpit.md` §1 (evidence categories),
  §2 (Work as a local destination), §8, §11
- `internal/store/project_work.go` — `ProjectWorkFact` (Task, Parent,
  OpenBlockers, BlockingPlans, `Lease`, `StateEvent`), `Blocked()`,
  `ListProjectWorkFacts(ctx, projectID)` — the board's spine; do not
  re-derive blockedness
- `internal/store/agent_sessions.go:459` — `ProjectAgentSession`,
  `OpenAgentSessionsForProject` and its honest definition of "open"
- `internal/store/changes.go` — `PullRequest`, `CIRun`,
  `CIRunsForSHAs(keys []RepoSHA)`; `TaskPRs` returns refs only, which is why
  task 2 adds a bulk PR reader
- `internal/store/session_usage.go:657` — `TaskCost`, whose summing SQL
  task 3 turns into a grouped bulk query; `CostTotal{Currency, Cost}`
- `internal/api/crew.go:34` — `crewPage`/`crewView`: the built project-local
  destination pattern (`projectHeader` for the sidebar frame, one fetch, one
  render)
- `internal/api/admin.go:701` — `bucketWorkFacts`: the org board's coarser
  bucketing; the run board does not change it
- `internal/ui/layout.templ:252` — `localNav` and the doc comment explaining
  the `#work` anchor stopgap this plan retires
- `internal/ui/cockpit.templ:127` — `workRow`: the owner-vs-delegate avatar
  treatment the board rows reuse
- `internal/api/metrics.go` — `worklode_web_home_renders_total`: the
  nil-safe render-metric pattern to copy

## Coverage: why each level is what it is

- **032 §8 `partial`, no `fullCoverageWith`** — this plan delivers the two
  §8 sentences whose facts exist: the six-group live view and the per-item
  fact list (minus "next expected signal", which requires a policy to know
  what is expected). The rest of §8 — the presets (Manual, Planning assist,
  Execute accepted plans, Bounded autopilot), scoped authority and its
  policy preview, the authorization/effective-policy provenance on automatic
  actions, the pre-run confirmation surface (bounds, budgets, agent pools,
  1Password readiness), retry/stop policy, and **Pause automation** — has no
  governed objects to render: no spec defines the automation-policy or run
  model (038 §5.1 explicitly defers the external agent runtime), so no plan
  can name a `fullCoverageWith` yet. The gap is declared here on purpose:
  closing it starts with a spec for automation policy, not with another
  plan against 032.
- **032 §10 `none`** — binding, not delivered: the board reflows at narrow
  widths, its tables scroll in labelled containers, and group headings are
  real headings, but this plan adds no accessibility scope beyond the
  surface it builds.
- **032 §11 `none`** — the standing rule: e2e drives public surfaces only.
  Slice 3's bounded unattended run is exactly the §8 remainder above and is
  not planned here.
- **032 §13 `none`** — covered by exclusion. §13 is the non-goals list; it
  binds every part of this series and will never be covered by any plan.
  Recorded so the aggregate coverage query reads it as bound rather than
  forgotten.

## Global Constraints

- **The six groups are 032 §8's, quoted exactly and in its order:**
  `Ready`, `Running`, `Waiting`, `Needs judgment`, `Failed`, `Completed`.
  No seventh group, no renames, no reordering.
- **The mapping from facts to groups is one pure function, pinned here:**
  - `merged`, `deployed_dev`, `deployed_prod`, `released` → **Completed**;
  - `abandoned` → **Failed**;
  - `in_review` → **Needs judgment**;
  - `in_progress` with an active lease → **Running**;
  - `in_progress` with no active lease → **Needs judgment** (its worker is
    gone without done/block — a human must decide reclaim vs abandon; the
    row says `lease expired` as its last-event line when that is the fact);
  - `ready` and `Blocked()` → **Waiting**;
  - `ready` and not blocked → **Ready**;
  - `draft` → excluded (not part of execution yet).
  Blockedness is `ProjectWorkFact.Blocked()` — never re-derived — so the
  board and `Claim` cannot disagree about what is pickable.
- **Active items get the §8 fact list; queued and terminal items stay
  lean.** Running and Needs judgment rows show owner, delegate (agent +
  version from the open session), lease age (now − `Lease.AcquiredAt`,
  relative), last durable event (`StateEvent.Type` + relative time), cost
  (per-currency totals), and PR/check state (open or merged PR, latest CI
  conclusion for its head SHA, source-linked). Ready and Waiting rows show
  title, owner, and — Waiting only — what holds them (blocker task ids,
  blocking plan numbers). "Next expected signal" is not rendered (see
  Coverage).
- **Terminal groups are bounded.** Failed and Completed each render the
  newest 10 rows by `StateEvent.At` (tasks with no state event sort last)
  plus one muted `and N more` line. The board is a live view, not the
  project's history.
- **Facts keep their evidence category (032 §1).** Everything on the board
  is declared (task fields) or observed (lease, session, PR, CI, cost);
  nothing here is recommended or user-reported, and the board must not
  invent health, readiness, or progress language on top of the facts.
- **No new state, no writes, no events.** The one route this plan adds is a
  session-gated GET. Pause, retry, dispatch, budgets: out of scope (see
  Coverage), and a reviewer should reject a task that sneaks a mutation in.
- **Exact spellings.** Page heading: `Work`. Group headings as pinned above.
  Empty board: `No work in this project yet.` Empty group: the group is
  omitted, never rendered empty (honest empty states, as everywhere in the
  cockpit). More-line: `and N more`. Route: `GET /projects/{id}/work`.
  Canonical URL: `/projects/{id}/work`. Store methods: `OpenPRsForProject`,
  `TaskCostsForTasks`. Metric: `worklode_web_run_board_renders_total`.
- **Route guarding:** `"GET /projects/{id}/work": guarded(permWebRead)` in
  `routeGuards`, registered like the other project-local GETs. Go 1.22's
  mux prefers the literal `work` segment over the `{section}` wildcard, so
  no registration-order footwork; `projectSections` does not list `work`
  and must not gain it.
- **ADR 036:** nothing crosses the `/api/v1` wire, so no `internal/model`
  type is added. View types are `internal/ui` package-locals; the two store
  readers scan into `internal/store` types.
- **Metrics** (spec 022): one nil-safe instrument in
  `internal/api/metrics.go` —
  `worklode_web_run_board_renders_total{outcome}`, outcome bounded to
  `rendered` | `empty`. Never a project or task id in a label.
- **Store and `internal/api` tests need Postgres with pgvector**
  (`TEST_POSTGRES_DSN`); they skip without it and a skipped run proved
  nothing. Pure tests (tasks 1, 4) and render tests (task 5) must not touch
  a database. `e2e/` drives public surfaces only — never a direct store
  write.
- **Every task leaves `go test -trimpath ./...` green** and ends in its own
  commit. Never bare `go test`/`go build` (CLAUDE.md). Commit messages
  describe the change, never the plan file, and carry no `Co-authored-by:`.

---

## Tasks

### Task 1 — Pure classification: fact row to run group

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New file `internal/api/runboard.go`. The group type and classifier, no
store calls, no HTTP:

```go
// runGroup is 032 §8's grouping of live work, in the spec's own order.
// runGroupNone marks a task the board excludes (draft: not execution yet).
type runGroup int

const (
	runGroupNone runGroup = iota
	runGroupReady
	runGroupRunning
	runGroupWaiting
	runGroupJudgment // "Needs judgment"
	runGroupFailed
	runGroupCompleted
)

// runGroupOf classifies one task's facts per the pinned table in the plan's
// Global Constraints. Blockedness is f.Blocked() — the claim path's own
// predicate — and "running" requires the active lease ListProjectWorkFacts
// attaches, so an in_progress task whose lease expired lands in Needs
// judgment rather than lying about a worker that is gone.
func runGroupOf(f store.ProjectWorkFact) runGroup
```

First test, new `internal/api/runboard_test.go`, table-driven, no database —
one case per pinned mapping row, plus the two that earn their keep:

```go
func TestRunGroupOf(t *testing.T) {
	lease := &store.Lease{}
	blocker := []store.TaskRef{{ID: "WL-1"}}
	cases := []struct {
		name  string
		fact  store.ProjectWorkFact
		want  runGroup
	}{
		{"ready unblocked", fact("ready", nil, nil), runGroupReady},
		{"ready blocked", fact("ready", nil, blocker), runGroupWaiting},
		{"in_progress leased", fact("in_progress", lease, nil), runGroupRunning},
		{"in_progress orphaned", fact("in_progress", nil, nil), runGroupJudgment},
		{"in_review", fact("in_review", nil, nil), runGroupJudgment},
		{"in_review blocked still judgment", fact("in_review", nil, blocker), runGroupJudgment},
		{"abandoned", fact("abandoned", nil, nil), runGroupFailed},
		{"merged", fact("merged", nil, nil), runGroupCompleted},
		{"deployed_prod", fact("deployed_prod", nil, nil), runGroupCompleted},
		{"released", fact("released", nil, nil), runGroupCompleted},
		{"draft excluded", fact("draft", nil, nil), runGroupNone},
	}
	// fact(state, lease, blockers) is a small local helper building the
	// store.ProjectWorkFact; blocking plans exercise Blocked() the same way.
}
```

- [ ] Write the test; watch it fail to compile
- [ ] Implement `runGroup` and `runGroupOf` in `internal/api/runboard.go`
- [ ] `go test -trimpath ./internal/api -run TestRunGroupOf -count=1` → `ok`
- [ ] Commit: `git add internal/api && git commit` — subject e.g.
      `Classify project work into 032 §8 run groups`

### Task 2 — Store: open and merged PRs per project, in bulk

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

`internal/store/changes.go` has per-PR and per-task readers but no bulk
project read, and the board must not fan out `PRsForTask` per row. Add, in
`changes.go` beside `TaskPRs`:

```go
// OpenPRsForProject returns every open or merged pull request bound to a
// task in projectID, newest UpdatedAt first. One query: pull_requests
// joined to tasks on task_id, filtered by tasks.project. Closed-unmerged
// PRs are out — the board shows the change that is or became the task's
// delivery, not every attempt.
func (s *Store) OpenPRsForProject(ctx context.Context, projectID string) ([]PullRequest, error)
```

Reuse `scanPR` and the existing column list; no new types. The caller pairs
these with `CIRunsForSHAs` (already bulk) for check state.

First test, `internal/store/changes_test.go`, real Postgres via the
package's existing test-store helper: seed two tasks in the project and one
in another project; upsert an open PR on task A, a merged PR on task B, a
closed-unmerged PR on task A, and an open PR on the other project's task;
assert the read returns exactly the first two, newest first, with `TaskID`
set; assert a project with no PRs returns an empty non-nil slice.

- [ ] Write `TestOpenPRsForProject`; watch it fail
- [ ] Implement `OpenPRsForProject`
- [ ] `go test -trimpath ./internal/store -run TestOpenPRsForProject -count=1`
      → `ok` (not SKIP)
- [ ] Commit: e.g. `Read a project's task-bound PRs in bulk`

### Task 3 — Store: costs for a set of tasks, in bulk

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

`TaskCost` (`internal/store/session_usage.go:657`) prices one task per
call. The board needs the same sum for every active task in one query. Add,
in `session_usage.go`:

```go
// TaskCostsForTasks returns per-currency cost totals for each of the given
// tasks, keyed by task id. Same pricing join as TaskCost's window-less
// path — agent session usage on the task's leases, priced by model_prices
// — grouped by task and currency in SQL rather than fanned out per task.
// A task with no priced usage has no entry. Never hardcode a rate.
func (s *Store) TaskCostsForTasks(ctx context.Context, taskIDs []string) (map[string][]CostTotal, error)
```

`len(taskIDs) == 0` returns an empty map without touching the database.
Only `Currency` and `Cost` need populating on the returned `CostTotal`s
(the board renders no token breakdown); document that on the method rather
than inventing a slimmer type.

First test, `internal/store/session_usage_test.go`, real Postgres,
following the seeding `TestTaskCost` already does (leases + sessions +
usage buckets + a `model_prices` row): two tasks with priced usage, one
without; assert the map has exactly the two, each total equal to what
`TaskCost` reports for the same task (assert equality against `TaskCost`'s
output — the two paths must not drift); assert the empty-input fast path.

- [ ] Write `TestTaskCostsForTasks`; watch it fail
- [ ] Implement `TaskCostsForTasks`
- [ ] `go test -trimpath ./internal/store -run TestTaskCostsForTasks -count=1`
      → `ok` (not SKIP)
- [ ] Commit: e.g. `Price a set of tasks in one query`

### Task 4 — Pure assembly: groups, bounds, per-item facts

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Still `internal/api/runboard.go`. Everything already fetched goes in, one
renderable view comes out; the whole §8 grouping contract is testable with
plain structs:

```go
// runBoardInputs is everything the board derivation reads, already fetched.
// Sessions, PRs, CI, and Costs are keyed/joinable by task id and head SHA;
// Now anchors the relative lease-age and event-time strings.
type runBoardInputs struct {
	Facts    []store.ProjectWorkFact
	Sessions []store.ProjectAgentSession
	PRs      []store.PullRequest
	CI       map[store.RepoSHA][]store.CIRun
	Costs    map[string][]store.CostTotal
	Now      time.Time
}

// assembleRunBoard derives the board: groups in the pinned §8 order, each
// omitted when empty; Running and Needs judgment rows carry the full §8
// fact list; Failed and Completed are bounded to the newest 10 by state
// event with an "and N more" count. nil when no task classifies into any
// group — the page then renders the empty-board line.
func assembleRunBoard(in runBoardInputs) *ui.RunBoardView
```

View types in `internal/ui/views.go`, plain structs (task 5 renders them):
`RunBoardView{Page PageProps; CanonicalURL string; Project CockpitProject;
Groups []RunGroupView}`, `RunGroupView{Label string; Rows []RunRowView;
More int}`, `RunRowView{TaskID, Title, TaskURL, Owner, Delegate, LeaseAge,
LastEvent, Holds, PRLabel, PRURL, CheckLabel string; Costs []string}` —
strings assembled here so the templ component stays dumb. Delegate is
`agent vN` from the task's open session; a Running task with no session row
renders an empty delegate, never a fabricated one. PR pairing: newest PR
whose `TaskID` matches; `CheckLabel` from the newest CI run on its head SHA
(`conclusion` when finished, `status` while not); both empty when no PR.

First test, `TestAssembleRunBoard` in `runboard_test.go`, table-driven, no
database. Pin at minimum:

- **order**: one task per group → groups render in exactly the pinned §8
  order with the pinned labels;
- **omission**: groups with no rows are absent, and an input with only a
  draft task → nil;
- **active detail**: a Running row carries owner, delegate, lease age,
  last event, costs, and PR/check labels from the maps; a Ready row carries
  none of the active-only fields;
- **waiting holds**: a Waiting row names its blocker ids and blocking plan
  numbers in `Holds`;
- **bounds**: 12 Completed tasks → 10 rows, `More == 2`, newest state
  event first, the no-event task last;
- **orphan wording**: an in_progress task with no lease lands in Needs
  judgment with `LastEvent` reporting the lease loss per the Global
  Constraints.

- [ ] Write `TestAssembleRunBoard` (subtests per bullet); watch it fail
- [ ] Add the ui view structs; implement `assembleRunBoard` and its row
      formatters
- [ ] `go test -trimpath ./internal/api -run TestAssembleRunBoard -count=1`
      → `ok`; `go test -trimpath ./internal/ui -count=1` → `ok`
- [ ] Commit: e.g. `Assemble the 032 §8 run board from project facts`

### Task 5 — The board page: templ component and stylesheet

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [4]
```

New `internal/ui/runboard.templ`: `templ RunBoard(v RunBoardView)` renders
inside the project sidebar frame exactly as `Crew` does (`crew.templ` is
the precedent — `Page` wrapper, shell with sidebar, `active` key `work` so
`localNav` marks Work current). One `<section class="card">` per group with
the group label as its heading and a count chip; rows reuse the `.wl`
worklist row shape and the owner-vs-delegate avatar treatment from
`cockpit.templ`'s `workRow`; active-row facts render as the muted second
line with the PR and check labels linking their source URLs; `More > 0`
renders the muted `and N more` line; a nil/empty view renders
`No work in this project yet.`

In `layout.templ`'s `localNav`: replace the anchor stopgap with
`@navLink("/projects/"+projectID+"/work", "Work", "work", active)` and cut
the doc-comment paragraph explaining it — the comment must describe what is
true after this change, short and precise.

Styles: extend the existing worklist/card sections of
`internal/ui/styles/app.tailwind.css` only if a rule is genuinely missing;
point at the stylesheet, do not transcribe it here.

First test, `internal/ui/views_test.go`: render `RunBoard` with a fixture
carrying one row in every group and assert: the six group labels appear in
the pinned order (index comparison on the rendered buffer); the Running
row's delegate, lease age, cost, and check label render; the Waiting row
names its blocker; `and 2 more` renders for a `More: 2` group; the Work
nav item carries `aria-current="page"`. Second subtest: an empty view
renders the empty-board line and no group headings.

- [ ] Write the render tests; watch them fail to compile
- [ ] Write `runboard.templ`, add the view wiring, fix `localNav`
- [ ] `go generate ./...` — `git status` shows `runboard_templ.go`,
      `layout_templ.go`, and `internal/ui/assets/app.css` regenerated
- [ ] `go test -trimpath ./internal/ui -count=1` → `ok`
- [ ] Commit **including the generated artifacts**: e.g.
      `Render the project run board`

### Task 6 — Tracer: `GET /projects/{id}/work`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 5]
```

The convergence point: route, guard row, handler, metric.

- `internal/api/router.go`: `"GET /projects/{id}/work": guarded(permWebRead)`.
- Registration beside the other project-local GETs, same wrapping as the
  crew page.
- Handler `runBoardPage` in `internal/api/runboard.go`, `crewPage`-shaped:
  `projectHeader` (unknown project 404s like every project page), then
  `ListProjectWorkFacts`, `OpenAgentSessionsForProject`,
  `OpenPRsForProject` + `CIRunsForSHAs` over the PR head SHAs, and
  `TaskCostsForTasks` over the tasks classified Running/Needs judgment
  (classify first, then price only the active set), then
  `assembleRunBoard`, then `renderWeb`. Record
  `worklode_web_run_board_renders_total`: `empty` when the view is nil,
  else `rendered`. Add the instrument to `internal/api/metrics.go`
  following `worklode_web_home_renders_total` (nil-safe method, bounded
  outcome consts, registered in `initMetrics`, covered by the existing
  metrics tests' pattern).

Tests in `internal/api/runboard_test.go` (Postgres, seeding through the
API/store test helpers the cockpit tests use):

- `TestRunBoardPage`: seed a project with a ready task, a blocked task, a
  claimed in_progress task with an open agent session and priced usage, an
  in_review task with an upserted open PR and a completed CI run, and a
  merged task; GET `/projects/{id}/work` and assert each pinned group
  heading present exactly once, the Running row shows the delegate and a
  cost, the Needs-judgment row links the PR and shows the CI conclusion,
  and the merged task appears under `Completed`.
- `TestRunBoardPageEmpty`: a project with no tasks → 200, the empty-board
  line, no group headings.
- `TestRunBoardPageUnknownProject`: → 404, matching `projectPage`'s
  behavior.

- [ ] Write the three tests; watch them fail (the route 404s)
- [ ] Add the guard row, registration, handler, and instrument
- [ ] `go test -trimpath ./internal/api -count=1` → `ok` (with Postgres up;
      `NewServer`'s boot check proves the guard row and route agree)
- [ ] `go test -trimpath ./...` → green
- [ ] Commit: e.g. `Serve the project run board at /projects/{id}/work`

### Task 7 — e2e and docs alignment

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [6]
```

- `e2e/smoke_test.go` `assertWebPages`: `GET /projects/{id}/work` on the
  smoke project returns 200 and contains the `Work` heading — through the
  public surface only, no store writes.
- Comment sweep adjacent to the change: `runboard.go`'s package-level doc
  states the §8 slice it renders and names the Coverage section of this
  plan for what §8 still awaits; `localNav`'s comment already fixed in
  task 5 — verify nothing else still describes the `#work` anchor
  (`grep -rn '#work' internal/`).
- Add **nothing** to `docs/follow-ups.md`: the §8 remainder is declared by
  this plan's `covers` block, and a second copy drifts.

- [ ] Extend `assertWebPages`;
      `go test -trimpath -race -count=1 -tags e2e ./e2e/ -run TestSmoke` →
      `ok` (local stack per CLAUDE.md)
- [ ] Comment sweep; `go test -trimpath ./...` → green
- [ ] Commit: e.g. `Prove the run board end to end`
