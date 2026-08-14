---
status: draft
covers:
  - spec: docs/specs/032-project-cockpit.md#sec-1
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-9
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
requires:
  - docs/plans/2026-08-14-cockpit-page-frame-unification.md
  - docs/plans/2026-08-14-project-crew-participants.md
  - docs/plans/2026-08-14-approvals-1-table-and-web-act.md
---
# Home as the project list — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /` stops rendering the org-wide board under a borrowed
"Current work" heading and becomes what spec 032 §9 makes it: the actor's
project list — a card grid sorted by what the actor needs to follow up on
(approvals awaiting them first, then projects they lead, then projects they
are on), with recent activity as the secondary sort. `GET /work` keeps the
org-wide board unchanged; it was always the task-oriented destination.

**Architecture:** `homePage` (`internal/api/web.go`) renders a new
`ui.Home(ui.HomeView)` instead of `ui.Board`. The card data comes from five
sources: `ProjectsForActor(actorID)` (plan B's crew reader),
`ApprovalsAwaiting(actorID, groups)` (plan C's per-project awaiting counts),
per-project task counts by state, participant initials, and
`max(tasks.updated_at)` as last activity. The state counts and last activity
come from the store's existing bulk reader `ListProjectWorkFacts` — see the
Decision section; no new counts reader is added. Card assembly is split into
a pure projection (`assembleHomeFacts`: which projects appear, with which
facts) and a pure derivation (tier, signal line, sort order), both
table-tested with no database, then joined over real data by the `homePage`
tracer. The page renders inside the unified `.shell` frame plan A builds.

**Tech Stack:** Go 1.26, `net/http` mux, `internal/api` handlers + `render.go`
seam, `templ` components in `internal/ui`, standalone Tailwind v4 build
(`internal/ui/styles/app.tailwind.css` → committed `app.css`). `internal/api`
and `internal/store` tests need Postgres with pgvector
(`TEST_POSTGRES_DSN`); they skip silently without it, and a skipped run
proves nothing.

**Read first:**
- `docs/specs/032-project-cockpit.md` §1, §9, §10
- `internal/api/web.go` — `homePage`, `workPage`, `renderBoard`
- `internal/api/admin.go:906` — `assembleBoard`, the bucketing this plan
  extracts and reuses
- `internal/store/project_work.go` — `ListProjectWorkFacts`, the one bulk
  work-facts reader
- `internal/api/render.go` and `internal/ui/views.go` — the api → ui view
  seam and its conventions
- `internal/api/web_test.go` — `assertShell`, `assertOneAriaCurrent`,
  `assertOrder`, `TestHomePage`, `TestWorkPageOrgBoard`
- `internal/api/oidcweb_test.go` — `TestAuthCallbackRoundTrip`, the
  login-round-trip pattern the actor-mode handler tests reuse

## Dependencies

This plan consumes the other three plans in the 2026-08-14 series and must
not start until all three have landed. When these documents are minted as
tasks, each of the three becomes a document-level `blocks` edge onto this
plan (never a task-number `blockedBy` — they are separate documents):

- **requires** `docs/plans/2026-08-14-cockpit-page-frame-unification.md`
  (A) — the `.shell` page frame and global sidebar Home renders inside.
- **requires** `docs/plans/2026-08-14-project-crew-participants.md` (B) —
  `project_participants`, `ProjectsForActor`, `ListParticipants`, and the
  `groups` claim stored on `actors` at login.
- **requires** `docs/plans/2026-08-14-approvals-1-table-and-web-act.md`
  (C) — the `approvals` table and `ApprovalsAwaiting(actorID, groups)`.

**Consumption is not coverage.** This plan reads B's and C's readers and
renders inside A's frame, but claims no `covers` on 029 §6 or §7 — those
sections belong to B and C. The `covers` block above names only what D
itself delivers or is bound by.

**Coordination point (declared unknown):** the exact Go signatures and row
shapes of `ProjectsForActor`, `ListParticipants`, `ApprovalsAwaiting`, and
`store.Actor`'s `groups`/`email` columns are fixed by B's and C's landed
code, not by this file. The shapes shown in the tasks below are the contract
from the approved design brief (`{project, roles, is_lead}` rows;
per-project awaiting counts). Adapt names mechanically; if a landed shape is
*materially* different (e.g. `ApprovalsAwaiting` does not return per-project
counts), stop and escalate — that is a plan defect for the planning tier,
not something to improvise around.

## Coverage: why each level is what it is

- **032 §9 `partial`** — this plan delivers the approvals-awaiting tier, the
  role-sorted project list, and the two honest degradations. Deferred (see
  Deferred below): the Morning Brief itself, its event-boundary cutoff, the
  "Reviewed through now" action, and Home's assigned-work and
  supervised-agent summaries. The org-wide board moves to `/work` framing
  only. No `fullCoverageWith` is named because no planned sibling finishes
  §9 — the remainder is a declared gap.
- **032 §10 `partial`** — covers only the narrow-width behaviour of the one
  surface it builds (the card grid collapsing to one column, keyboard-
  operable whole-card links, no colour-only meaning on the counts strip).
- **032 §1 `none`** — binding, not delivered: the card projection must not
  invent status. Every count is derived from task state through the same
  bucketing the board uses; last activity is `max(tasks.updated_at)`; a
  project with no data renders an honest absence, never a fabricated value.
  This plan builds no evidence-category presentation.
- **032 §11 `none`** — the standing rule: e2e tests drive the HTTP UI and
  API surfaces and never write directly to the store. The three-slice
  release content is not planned here.

## Decision: reuse the board's facts reader — no new counts reader

`assembleBoard` (`internal/api/admin.go`) already computes per-project state
buckets from `store.ListProjectWorkFacts`, including the one non-obvious
rule: **Blocked = `state == "ready"` with at least one open blocker**
(`ProjectWorkFact.Blocked()`), not a `blocked` task state. A second SQL
counts reader would inevitably re-implement that predicate and drift.

So Home adds no store reader for counts or activity. Task 1 extracts the
bucketing out of `assembleBoard` into a pure `bucketWorkFacts` function and
refactors `assembleBoard` onto it in the same task; Home computes its
three-count strip as `len()` over the same buckets and last activity as the
max `Task.UpdatedAt` over the same facts (which include done tasks —
`ListProjectWorkFacts` has no state filter, verified). One reader, one
"blocked" definition, two surfaces. The only new store surface is the bulk
(all-projects) form of B's participants reader (task 5), because rendering
crew avatars for every card via a per-project reader would be an N+1.

## Deferred — the rest of 032 §9

Stated per the approved design brief; none of these appear in any task:

- the Morning Brief itself (grouping, ordering, collapsed routine work);
- the actor's event-boundary cutoff and its persistence;
- the explicit **Reviewed through now** action;
- Home's assigned-work and supervised-agent summaries.

## Global Constraints

- **Exact tier order** (approved design, quoted): tier 1 = approvals
  awaiting you; tier 2 = projects you lead; tier 3 = projects you are on.
  Sort is tier ascending, then last activity descending, ties broken by
  project ID ascending. Open mode has no tiers: last activity descending,
  ties by project ID ascending.
- **Exact spellings.** Role badges: `Lead`, `Member`. Signal lines:
  `1 approval awaiting you` / `N approvals awaiting you`,
  `You lead this project`, `You are on this project`. Counts strip labels:
  `In progress`, `In review`, `Blocked` (matching `ui.StateLabel`).
  Activity line: `Last activity ` + `ui.FmtTime`, or `No activity yet` for
  a project with no tasks. Empty state (actor on no projects):
  `You are not on any project yet.` with a link labelled
  `Browse all projects` to `/projects`. Open mode with zero projects:
  `No projects yet.` Never a fabricated card.
- **Degradations are honest, both required.** (a) No actor
  (`Subject.ActorID == ""`, which persists behind `LODE_WEB_OPEN=true` for
  compose and CI — `docs/plans/2026-08-14-web-ui-requires-a-login-provider.md`):
  list **all** projects by last activity, no role badge, no signal line.
  (b) An actor on no projects (and with nothing awaiting them) gets the
  empty state above.
- **Grid is deterministic.** `.homegrid` is a fixed
  `grid-template-columns:1fr 1fr`, collapsing to `1fr` at
  `@media(max-width:820px)`. Never `auto-fit`/`auto-fill` — "two on a
  laptop" is guaranteed, not emergent. The whole card is one `<a>` to
  `/projects/{id}`; no nested interactive elements inside it (032 §10:
  keyboard-operable, visible focus via the existing `:focus-visible` rule).
- **Preserve the tested shell invariants**: exactly one
  `aria-current="page"` per page (`assertOneAriaCurrent`), the shell
  markers (`assertShell`), and every `/work` assertion. Home keeps
  `ActiveGlobal: "home"`; the route (`GET /{$}`), its `routeGuards` entry
  (`permWebRead`), and `navWrap("home", ...)` are all unchanged — no new
  route, no new guard.
- **Metrics** (spec 022, `docs/specs/022-prometheus-metrics.md`): the one
  new instrument is `worklode_web_home_renders_total{mode}` with mode
  bounded to exactly `actor` | `open` | `empty` — never a project or task
  id in any label. Nil-safe method on `internal/api/metrics.go`'s struct,
  `prometheus.Registerer` already threaded; test alongside the existing
  metrics tests. `navWrap` already counts the `home` destination by
  outcome; do not duplicate it.
- **Toolchain is fixed** by 032 §12 as delivered by
  `docs/plans/2026-08-10-cockpit-templ-htmx-tailwind.md`: templ components
  compiled by `go generate ./...` into committed `*_templ.go`, styles in
  `internal/ui/styles/app.tailwind.css` compiled into committed
  `internal/ui/assets/app.css`. Never hand-edit a generated artifact; every
  task that touches a `.templ` or the stylesheet runs `go generate ./...`
  and commits the regenerated artifacts. Point at the stylesheet; this plan
  deliberately does not transcribe it.
- **Dependency direction**: `internal/ui` depends on nothing beyond stdlib,
  the store types it embeds (today `internal/store`; `internal/model` if
  the one-model migration has landed by execution time), and the templ
  runtime. `internal/api` imports `internal/ui`, never the reverse. All
  api-side assembly lives in `internal/api` (`home.go`), mapped through the
  `render.go` seam conventions.
- **One reader per fact family.** Counts and activity come only through
  `bucketWorkFacts` over `ListProjectWorkFacts`; participants only through
  the (extended) `ListParticipants`; membership only through
  `ProjectsForActor`; awaiting only through `ApprovalsAwaiting`. Never a
  second computation of "blocked".
- **Store/api tests need Postgres with pgvector**; a green run without it
  skipped and proved nothing (`TEST_POSTGRES_DSN` — see CLAUDE.md). Pure
  tests (tasks 1–4) must not require a database. `e2e/` drives public
  surfaces only — never a direct store write.
- **Every task leaves `go test ./...` green** and ends in its own commit.
  Commit messages describe the change, never the plan file, and never carry
  `Co-authored-by:` trailers.

---

## Tasks

### Task 1 — Extract the board's bucketing into `bucketWorkFacts` and refactor `assembleBoard` onto it

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

In `internal/api/admin.go` (next to `assembleBoard`), extract the per-fact
switch into a pure function over one project's facts, and add the activity
helper Home needs:

```go
// workBuckets is the per-project bucketing of work facts shared by the
// board (assembleBoard) and Home's card counts — the one place "blocked"
// is computed for a web surface. Blocked means a ready task with at least
// one open blocker (ProjectWorkFact.Blocked); in_progress and in_review
// tasks bucket by state; done tasks bucket nowhere.
type workBuckets struct {
	InProgress, InReview, Ready, Blocked []store.ProjectWorkFact
}

func bucketWorkFacts(facts []store.ProjectWorkFact) workBuckets

// lastActivity returns the newest Task.UpdatedAt across facts (all states,
// done included), or the zero time for an empty slice.
func lastActivity(facts []store.ProjectWorkFact) time.Time
```

Refactor `assembleBoard`'s inner loop to call `bucketWorkFacts` on
`byProject[p.ID]` and map each bucket to its `boardTaskJSON` slice (the
in-progress holder mapping stays where it is, reading `f.Lease` off the
bucketed facts). Behaviour must be identical: same buckets, same order
(`bucketWorkFacts` preserves input order within each bucket).

First test, in a new `internal/api/home_test.go` (no database — plain
structs):

```go
func TestBucketWorkFacts(t *testing.T) {
	facts := []store.ProjectWorkFact{
		{Task: store.Task{ID: "WL-1", State: "in_progress"}},
		{Task: store.Task{ID: "WL-2", State: "in_review"}},
		{Task: store.Task{ID: "WL-3", State: "ready"}},
		{Task: store.Task{ID: "WL-4", State: "ready"},
			OpenBlockers: []store.TaskRef{{ID: "WL-3"}}},
		{Task: store.Task{ID: "WL-5", State: "done"}},
	}
	b := bucketWorkFacts(facts)
	ids := func(fs []store.ProjectWorkFact) []string { /* map to Task.ID */ }
	// InProgress [WL-1], InReview [WL-2], Ready [WL-3], Blocked [WL-4];
	// WL-5 appears nowhere.
}
```

Add a `TestLastActivity` table test: empty slice → zero time; mixed states →
the max `UpdatedAt` even when it belongs to a done task.

- [ ] Write `TestBucketWorkFacts` + `TestLastActivity`; watch them fail to compile
- [ ] Implement `bucketWorkFacts` and `lastActivity`
- [ ] Refactor `assembleBoard` onto `bucketWorkFacts`; no behaviour change
- [ ] `go test ./internal/api -run 'TestBucketWorkFacts|TestLastActivity' -count=1`
      → `ok  github.com/sunstoneinstitute/worklode/internal/api`
- [ ] With Postgres up: `go test ./internal/api -count=1` → `ok` (board tests
      prove the refactor changed nothing)
- [ ] Commit: `git add internal/api && git commit` — subject e.g.
      `Extract board bucketing into bucketWorkFacts`

### Task 2 — Home view types and activity helper in `internal/ui`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Add to `internal/ui/views.go` (following the existing section-comment
style):

```go
// --- home project list -------------------------------------------------------

// HomeView is the Home project list (spec 032 §9, first slice). Mode is
// "actor" (signed-in, has cards), "open" (no actor — all projects, no role
// badge or signal), or "empty" (an actor on no projects); it also labels the
// worklode_web_home_renders_total metric, so the three values are fixed.
type HomeView struct {
	Page  PageProps
	Mode  string
	Cards []HomeCard
}

// HomeCard is one project card, density B: identity, role badge ("Lead",
// "Member", or "" when the viewer has no role), the one-line signal saying
// why the card sits where it does ("" in open mode), the three-count strip,
// up to five crew initials plus an overflow count, and last activity (zero
// time = no tasks yet). The whole card links to /projects/{ProjectID}.
type HomeCard struct {
	ProjectID, Name, Key string
	RoleBadge            string
	Signal               string
	InProgress, InReview, Blocked int
	CrewInitials         []string
	CrewMore             int
	LastActivity         time.Time
}
```

And the activity formatter next to the other presentation helpers:

```go
// HomeActivity renders a card's last-activity line, honest about absence.
func HomeActivity(t time.Time) string {
	if t.IsZero() {
		return "No activity yet"
	}
	return "Last activity " + FmtTime(t)
}
```

First test, new `internal/ui/views_test.go` (package `ui`, no database):
table-test `HomeActivity` — zero time → `No activity yet`; a fixed
timestamp → `Last activity 2026-08-14 09:30`.

- [ ] Write the `HomeActivity` table test; watch it fail
- [ ] Add the types and helper
- [ ] `go test ./internal/ui -count=1` →
      `ok  github.com/sunstoneinstitute/worklode/internal/ui`
- [ ] Commit: `git add internal/ui && git commit` — e.g.
      `Add Home project-list view types`

### Task 3 — Pure projection: `assembleHomeFacts`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

New file `internal/api/home.go`. The projection decides **which projects get
a card and with what facts** — no ordering, no tier, no strings:

```go
// homeInputs is everything Home's card assembly reads, already fetched.
// Membership/Awaiting are empty maps in open mode. Field types for
// Membership and Participants follow plan B's landed row shapes.
type homeInputs struct {
	Projects     []store.Project                    // every project
	Facts        map[string][]store.ProjectWorkFact // by project id
	Membership   map[string]memberFacts             // from ProjectsForActor
	Awaiting     map[string]int                     // from ApprovalsAwaiting
	Participants map[string][]string                // display names, lead first
	OpenMode     bool
}

// memberFacts is the viewer's relationship to one project.
type memberFacts struct{ IsLead bool }

// homeCardFacts is one project's assembled facts, before tiering.
type homeCardFacts struct {
	Project      store.Project
	IsMember, IsLead bool
	Awaiting     int
	InProgress, InReview, Blocked int
	CrewNames    []string
	LastActivity time.Time
}

// assembleHomeFacts projects the inputs onto cards-to-be. Actor mode: the
// union of projects the actor is on and projects with approvals awaiting
// them (a card can exist for a non-member — it then has no role). Open
// mode: every project. Counts come from bucketWorkFacts, activity from
// lastActivity — the same derivations the board uses.
func assembleHomeFacts(in homeInputs) []homeCardFacts
```

First test in `internal/api/home_test.go`, table-driven, no database. Pin at
minimum:

- actor mode: member-only project, lead project, and a **non-member project
  with `Awaiting > 0`** all get cards; an unrelated project gets none;
- open mode: every project gets a card, `IsMember`/`IsLead` false,
  `Awaiting` 0 even if the map has entries (open mode never loads them —
  assert the projection ignores rather than trusts);
- actor on no projects and nothing awaiting → empty slice (the empty state
  is real absence, never fabricated);
- counts respect the bucketing rules (a ready task with an open blocker
  counts as Blocked, a done task counts nowhere but still moves
  `LastActivity`);
- a project with no tasks → zero `LastActivity`.

- [ ] Write `TestAssembleHomeFacts` (subtests per bullet); watch it fail
- [ ] Implement `assembleHomeFacts` in `internal/api/home.go`
- [ ] `go test ./internal/api -run TestAssembleHomeFacts -count=1` → `ok`
- [ ] Commit: e.g. `Project Home card facts from work, crew and approval readers`

### Task 4 — Pure derivation: tier, signal, and sort

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

Still `internal/api/home.go` — the one-decision rule and the strings, ending
in ordered `[]ui.HomeCard`:

```go
// homeTier: 1 = approvals awaiting the actor, 2 = the actor leads,
// 3 = the actor is on the project. Actor mode only.
func homeTier(f homeCardFacts) int {
	switch {
	case f.Awaiting > 0:
		return 1
	case f.IsLead:
		return 2
	default:
		return 3
	}
}

// homeSignal is the card's "why this card is here" line ("" in open mode).
func homeSignal(f homeCardFacts) string   // exact spellings: Global Constraints

// homeCards derives tier + signal, maps facts to ui.HomeCard (RoleBadge
// "Lead"/"Member"/""; CrewNames -> up to five ui.Initials + CrewMore), and
// sorts: actor mode by (tier asc, LastActivity desc, project ID asc); open
// mode by (LastActivity desc, project ID asc).
func homeCards(in homeInputs) []ui.HomeCard
```

First test (`TestHomeCardsOrderAndSignals`, no database): three actor-mode
projects — one awaiting (`Awaiting: 2`, not a member), one led, one plain
membership — plus differing activity times. Assert the exact output order,
`Signal` strings (`2 approvals awaiting you`, `You lead this project`,
`You are on this project`), `RoleBadge` (`""`, `Lead`, `Member`), and the
singular `1 approval awaiting you`. A second subtest pins open mode:
activity-descending order, ID tiebreak, every `Signal`/`RoleBadge` empty.
A third pins crew truncation: seven names → five initials + `CrewMore: 2`,
lead's initials first.

- [ ] Write `TestHomeCardsOrderAndSignals`; watch it fail
- [ ] Implement `homeSignal`, `homeTier`, `homeCards` (use `sort.SliceStable`)
- [ ] `go test ./internal/api -run TestHomeCards -count=1` → `ok`
- [ ] Commit: e.g. `Derive Home card tiers, signals and sort order`

### Task 5 — Bulk participants read for Home

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Home renders crew avatars on every card; a per-project `ListParticipants`
call per card is an N+1. Extend plan B's landed participants reader in
`internal/store` with the repo's established `"" means all` form (the
convention `ListProjectWorkFacts` and `ListIssues` already follow) rather
than adding a parallel reader: `ListParticipants(ctx, "")` returns every
row, and the row type carries `ProjectID` so callers can group. Ordering
must be deterministic and lead-first per project:
`ORDER BY project_id, is_lead DESC, actor_id`. Refactor — do not add a
second query that joins `project_participants` its own way. If B's landed
signature differs materially from `ListParticipants(ctx, projectID)`
(see the Dependencies coordination note), escalate before improvising.

First test, `internal/store` (real Postgres; skips without it — run with
Postgres up or the task proved nothing):

```go
func TestListParticipantsAllProjects(t *testing.T) {
	s := newTestStore(t) // per this package's existing helper
	// seed two projects; add lead + member to proj1, one member to proj2
	// via B's AddParticipant
	rows, err := s.ListParticipants(ctx, "")
	// assert: 3 rows; grouped proj1 before proj2; proj1's lead row first;
	// every row carries its ProjectID
}
```

Metrics: extend the instrument B gave this reader (or its store-metrics
family) only if the bulk form introduces a new outcome; do not mint a new
metric name for an argument change.

- [ ] Write `TestListParticipantsAllProjects`; watch it fail
- [ ] Extend the reader + row type; update B's existing callers if the row
      shape changed (compile is the check)
- [ ] `go test ./internal/store -run TestListParticipants -count=1` →
      `ok  github.com/sunstoneinstitute/worklode/internal/store` (not `SKIP`)
- [ ] `go test ./... ` → green
- [ ] Commit: e.g. `Add all-projects form to ListParticipants`

### Task 6 — Home templ component and the two-column card grid

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

New `internal/ui/home.templ`. Render through `@Page(v.Page)` inside the same
wrapper the other post-plan-A global pages use (match `projects.templ` after
A's conversion — do not restructure the shell). Shape:

```
templ Home(v HomeView) {
	@Page(v.Page) {
		... <h1>Home</h1>
		if v.Mode == "empty" {
			<p class="muted">You are not on any project yet.
				<a href="/projects">Browse all projects</a></p>
		} else if len(v.Cards) == 0 {
			<p class="muted">No projects yet.</p>
		} else {
			<div class="homegrid">
				for _, c := range v.Cards { @homeCard(c) }
			</div>
		}
	}
}
```

`homeCard(c HomeCard)` is one `<a class="homecard" href={ "/projects/" +
c.ProjectID }>` containing: name + `Key` chip + role-badge chip (omitted
when `RoleBadge == ""`), the signal line (omitted when empty), the counts
strip (`In progress N · In review N · Blocked N` — label text plus number,
never colour alone), the crew row (`.avatar` spans from `CrewInitials`,
then a `+N` chip when `CrewMore > 0`), and `HomeActivity(c.LastActivity)`.
Reuse the existing primitives (`.chip`, `.avatar`, `.muted`, `.small`).

Styles go in a new `/* ---------- home project grid ---------- */` section
of `internal/ui/styles/app.tailwind.css` (do not copy other sections; the
stylesheet is the reference). The two load-bearing rules, exactly:

```css
.homegrid{display:grid;grid-template-columns:1fr 1fr;gap:14px}
@media(max-width:820px){.homegrid{grid-template-columns:1fr}}
```

plus `.homecard` (block link on `--surface`, `--line` border, radius 12px,
`--shadow-sm`, `color:var(--ink)`, no underline on hover) and whatever small
internal-row rules the card needs — all on existing custom properties, both
themes covered for free.

First test in `internal/ui/views_test.go`: render `Home` for each mode into
a buffer (`ui.Home(v).Render(context.Background(), &buf)`) and assert: the
card `<a>` carries `href="/projects/p1"`; open-mode output contains no
role-badge chip and no signal line; `Mode == "empty"` output contains
`You are not on any project yet.` and `href="/projects"` and no
`class="homecard"`.

- [ ] Write the render test; watch it fail to compile
- [ ] Write `home.templ` and the stylesheet section
- [ ] `go generate ./...` — regenerates `home_templ.go` and
      `internal/ui/assets/app.css`; `git status` shows both
- [ ] `go test ./internal/ui -count=1` → `ok`
- [ ] Commit **including the generated artifacts**: e.g.
      `Add Home project-card component and grid`

### Task 7 — Tracer: `homePage` renders the project list

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [4, 5, 6]
```

Rewire `homePage` in `internal/api/web.go` (route, guard, and
`navWrap("home", ...)` untouched):

```go
func (s *server) homePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sub := subjectFrom(r)
	// Fetch: ListProjects, ListProjectWorkFacts(ctx, ""), and the bulk
	// ListParticipants(ctx, "") — grouped by project id into homeInputs.
	// Actor mode (sub.ActorID != ""): add ProjectsForActor and
	// ApprovalsAwaiting(sub.ActorID, groups), groups read from the
	// actor row (GetActor — plan B stores the Keycloak claim there).
	// Open mode: OpenMode = true, membership/awaiting left empty.
	cards := homeCards(in)
	mode := "actor" // or "open"; "empty" when actor mode yields no cards
	s.observeHomeRender(mode)
	view := ui.HomeView{Page: ui.PageProps{Title: "worklode: home",
		ActiveGlobal: "home"}, Mode: mode, Cards: cards}
	// Content-Type + ui.Home(view).Render — same error handling as the
	// other page handlers (webStoreErr on store errors, log on render).
}
```

Every store error goes through `s.webStoreErr` — never a half-rendered
page. Add `observeHomeRender(mode string)` to `internal/api/metrics.go`
(nil-safe, `worklode_web_home_renders_total`, mode label bounded per Global
Constraints) with a test following `metrics_internal_test.go`'s pattern.
Update `homePage`'s doc comment and the stale §9 sentence in `web.go`'s
comments.

Tests in `internal/api/web_test.go` (Postgres required):

- Rewrite `TestHomePage` → open mode via `newTestServer`: two projects with
  tasks giving proj2 the newer activity; assert `assertShell`,
  `assertOneAriaCurrent`, `<h1>Home</h1>`, **no** `Current work`, and — via
  `assertOrder` — that `href="/projects/proj2"` appears before
  `href="/projects/proj1"`; assert no `You lead` / `awaiting you` /
  role-badge text anywhere in `mainContent`.
- `TestHomePageActorTiers` via `newOIDCServer`: perform the login round
  trip from `TestAuthCallbackRoundTrip` (factor it into a
  `webLogin(t, h, iss, claims) string` helper returning the session cookie
  unless plan C already landed one — reuse C's if so). Seed membership/lead
  via B's store methods and an awaiting approval via C's write path, then
  GET `/` with the cookie and assert card order (awaiting project first
  despite older activity), the three exact signal lines, and badges.
- `TestHomePageEmptyState`: logged-in actor, projects exist but the actor
  is on none → `You are not on any project yet.`, `href="/projects"`, and
  no `homecard` in `mainContent`.

- [ ] Write the three tests; watch them fail
- [ ] Implement `homePage` + `observeHomeRender` (+ metrics test)
- [ ] `go test ./internal/api -count=1` → `ok` (with Postgres up)
- [ ] Commit: e.g. `Render Home as the actor's project list`

### Task 8 — Retire the board's Home framing; `/work` keeps the board

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [7]
```

The board no longer has two callers, so its Home branch is dead code:

- `internal/ui/views.go`: delete `BoardView.IsHome`.
- `internal/ui/board.templ`: delete the `if v.IsHome` branch — the page is
  always `<h1>Work</h1>`, no `Current work` heading anywhere.
- `internal/api/render.go`: drop `isHome` from `boardView`'s signature.
- `internal/api/web.go`: collapse `renderBoard` into `workPage` (it has one
  caller left; keep the inbox count and `ui.Board` render exactly as they
  are) and delete the now-unused `renderBoard`.
- Tests: `TestWorkPageOrgBoard` and the e2e `/work` assertions must pass
  **unchanged** — they are the proof `/work` kept the board. Update only
  compile-level fallout from the signature change.

- [ ] Make the deletions; `go generate ./...` for the templ change
- [ ] `go test ./internal/api ./internal/ui -count=1` → `ok`, with
      `TestWorkPageOrgBoard` untouched
- [ ] `grep -rn "Current work" internal/` → no matches
- [ ] Commit (with regenerated artifacts): e.g.
      `Drop the board's borrowed Home framing`

### Task 9 — e2e journey and docs alignment

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [7, 8]
```

`e2e/smoke_test.go` `assertWebPages` currently checks `GET /` for the shell
and `<h1>Home</h1>` only. Extend it through the public surface (the e2e
server runs without a session, i.e. the open-mode degradation): after the
existing shell checks, assert the home body contains the seeded project's
name (`Demo`) and a `href="/projects/` link to it, and does **not** contain
`Current work`; leave every `/work` assertion exactly as it is (that block
is the journey's proof the board still lives there). No store writes — the
suite seeds through the API as today.

Docs alignment:

- Add **nothing** to `docs/follow-ups.md`. The deferred remainder of 032 §9
  (Morning Brief, event-boundary cutoff, "Reviewed through now",
  assigned-work and supervised-agent summaries) is exactly what this plan's
  `coverage: partial` on §9 already declares, in a form a coverage query can
  read. A second copy in `docs/follow-ups.md` is unqueryable and drifts from
  the first.
- Sweep `internal/api/web.go` / `internal/ui` comments for leftover claims
  that Home shows the board (the package comment's page list included).
  Keep the edits short and precise — no debugging diary.

- [ ] Extend `assertWebPages`; run
      `go test -race -count=1 -tags e2e ./e2e/ -run TestSmoke` → `ok` (with
      the local stack per CLAUDE.md; the full suite otherwise)
- [ ] `go test -race -count=1 -tags e2e ./e2e/` → `ok`
- [ ] Docs sweep + follow-ups entry
- [ ] Commit: e.g. `Cover the Home project list in the e2e journey`
