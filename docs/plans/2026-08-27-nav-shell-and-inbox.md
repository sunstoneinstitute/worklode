---
status: draft
covers:
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-0
    coverage: none
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-1
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-2
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-3
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-3.1
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-3.2
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-3.3
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-3.4
    coverage: none
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-4
    coverage: full
  - spec: docs/specs/056-nav-shell-and-cross-project-inbox.md#sec-5
    coverage: none
---
# Nav shell and the cross-project inbox

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** spec 056, whole: the global navigation merges into the top bar and
shrinks to five destinations (§1); the task detail page gains the project
sidebar every other project-scoped page has (§2); `GET /inbox` lists what is
waiting on the signed-in actor, bucketed by §3.2's order and ranked inside
the work buckets by the existing det-v1 function fed all-projects facts then
filtered to membership (§3.3); and the top bar carries the inbox dot on
every page, computed once per request (§4). No new permission, role, stored
state, migration, or ontology term — the spec's own §5 list, and this plan
adds none either.

**Coverage:** §0 and §5 are purpose and non-goals (`none`). §3.4's decisions
bucket is explicitly not built (`none` — WL-333 tracks it, gated on 025
§10). Everything else is `full` here: this is a one-plan spec, per WL-334.

**Read first:**
- `docs/specs/inlined/056-nav-shell-and-cross-project-inbox.md` — whole
- `internal/ui/layout.templ` — the shell this plan reshapes: `Page` (topbar,
  brand, `.top-right` icon buttons), `primaryNav` (the eight destinations
  and the below-880px More control), `globalShell`, `projectShell`,
  `navLink`/`globalLink`/`globalIcon`
- `internal/api/web.go:447` — `taskPage`, today rendered without the
  project sidebar; `internal/api/web.go:192` — `homePage`, whose
  `ListProjectWorkFacts(ctx, "")` all-projects read is §3.3's exact feed
- `internal/api/cockpit_rank.go` — `rankSecondaryConcerns` (det-v1); it
  already takes a plain facts slice with no project scoping, which is why
  §3.3 needs no change to it
- `internal/store/participants.go:137` — `ProjectsForActor` (role, is_lead,
  is_deputy per project): the membership read
- `internal/store/approvals.go` — `prEntityJoin`, `ListAwaitingApprovals`
  (the queue reader whose join shape the inbox reader reuses),
  `ActorIDForGitHubLogin` (tx-scoped; see the Decisions note on authorship)
- `internal/api/authz.go:460` — `subjectFrom(r)`: the session subject,
  readable anywhere including `renderWeb`
- `internal/api/webform.go:165` — `renderWeb`, the one render entry point
  every web handler calls: where §4's once-per-request computation lands
- `internal/api/web_test.go` — the nav invariants (`TestGlobalNavOrder`,
  `assertOneAriaCurrent`) this plan re-pins
- `docs/research/cockpit-exception-ranking.md` §4/§6 — det-v1's definition

## Global Constraints

- **Exact spellings.** Global destinations after §1, in order:
  `Ideas`, `Intake`, `Projects`, `Work`, `Knowledge` — keys `ideas`,
  `intake`, `projects`, `work`, `knowledge`. Below-880px tiers: `ideas`,
  `projects`, `work` primary; `intake`, `knowledge` under More (the tiers
  they already have; More expands "the rest" per §1). The brand links to
  `/`. Inbox route: `GET /inbox`, guard `guarded(permWebRead)`, nav key
  `""` (it is not a destination). Bucket headings, in §3.2's order:
  `Reviews for you`, `Unassigned reviews in projects you lead`,
  `Your PRs awaiting others`, `Work assigned to you`, `Work you created`,
  `In progress near you`. Empty page line: `Nothing is waiting on you.`
  Indicator: an `iconbtn` anchor to `/inbox` immediately left of the theme
  toggle, `aria-label="Inbox"`, with a `dot-alert` variant of the shell's
  existing dot treatment when items exist and no dot otherwise. Metric:
  `worklode_web_inbox_renders_total{outcome}`, `outcome ∈ {rendered,
  empty}`.
- **The §3.2 bucket order and the §3.3 feed order are the spec's substance.**
  Buckets render in exactly the order above; the work buckets are ordered by
  det-v1 scored over the all-projects facts and filtered to membership
  afterwards — never filter first (§3.3 explains why, and a test pins it).
  Review buckets order oldest-open first by approval creation time, ties by
  approval id. `rankSecondaryConcerns` itself is not modified.
- **Active task states** for §3.1's "in-progress work" are `ready`,
  `in_progress`, and `in_review` — the open half of the task state CHECK.
  Pinned here so the reader and the tests agree.
- **The inbox is ordering, not access** (§3): no permission constant, no
  role, no filter on what an actor may read. Every listed item links to a
  page that exists and is independently reachable.
- **§4's two constraints hold structurally.** The has-items answer is
  computed exactly once per request, in `renderWeb`, from `subjectFrom(r)`,
  and reaches the top bar through a typed templ context value
  (`ui.WithInboxDot(ctx, bool)` / read by `Page`) — PageProps is unchanged
  and no view or component recomputes it. The store answer is one
  `EXISTS`-shaped query (`HasInboxItems`) that stops at the first item; the
  full ranked query runs only in the `/inbox` handler.
- **No change to `/`, `/reviews`, `/deliveries` routes, pages, or
  permissions** (§1): they leave the destination list only. `/` stays the
  post-login landing page. Spec 020's `/api/v1/inbox*` is untouched.
- **One model (ADR 036):** the inbox page is web-only — no `/api/v1` route,
  so its view types are `internal/ui` package-locals; no `internal/model`
  type is added.
- **Store and `internal/api` tests need Postgres with pgvector**
  (`TEST_POSTGRES_DSN`); a skipped run proved nothing. Pure assembly and
  render tests must not touch a database. `e2e/` drives public surfaces
  only.
- **Every task leaves `go test -trimpath ./...` green**, regenerates
  `go generate ./...` artifacts when a `.templ` or the stylesheet changes
  (commit them), and ends in its own commit. Never bare `go test`.

## Decisions this plan executes (settled here; do not reopen)

- **PR authorship resolves through the actor row, read once.** §3.1 says the
  correspondence is the GitHub-login column 029 §7 uses and notes the inbox
  reads it outside a transaction. The cheapest non-transactional form is not
  a new lookup function: the inbox reader already loads the acting actor
  (`GetActor`), whose row carries the login; "owned by the actor" is
  PR author login == that login, case-insensitively. One row read, no new
  query shape. **Cross-plan note:** the draft crew-lifecycle plan
  (`2026-08-25-research-work-6-crew-lifecycle`) renames
  `expected_github_login` to `github_username`; whichever lands second
  follows the column spelling it finds — the comparison is by the Actor
  struct field either way.
- **The task page renders zero `aria-current="page"`, and the invariant
  test says so.** §2: the task page's local navigation marks none of the
  sidebar destinations current, and its global nav passes `""` like every
  project page. `assertOneAriaCurrent`'s "never zero, never two" becomes
  "never two; zero only on the task detail page", asserted with the task
  page named — an undocumented zero anywhere else still fails.
- **Below-880px tiers stay where they are.** The five survivors keep their
  existing primary/secondary classes (three primary + More expanding two),
  so `nav.js`, the flex-order grouping, and the More toggle's active-state
  expression only shrink; nothing is redesigned. The More button's
  `templ.KV("active", ...)` expression updates to the two remaining
  secondary keys.
- **The inbox reader is one bulk read per fact family, bucketing is pure.**
  A new `store` reader returns the open `pr` approvals org-wide with their
  project id, PR title/URL, author login, and `required_actor` (the
  `prEntityJoin` shape `ListAwaitingApprovals` already uses, without its
  membership assumptions); work facts come from the existing
  `ListProjectWorkFacts(ctx, "")`; membership from `ProjectsForActor`. A
  pure `assembleInbox` does all classification, ordering, and filtering, so
  §3.2/§3.3 are table-tested with no database.
- **The dot is allowed to cost one bounded query per page render.** §4
  accepts this by design ("computed once per request"). `HasInboxItems`
  is one SQL statement of `EXISTS` unions with `LIMIT 1` semantics, checked
  only when the request has an authenticated subject; open-mode and
  unauthenticated renders skip it and render no dot.

## Tasks

### Task 1 — One navigation row in the top bar

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [ ]
```

`internal/ui/layout.templ` and `internal/ui/styles/app.tailwind.css`:

- Move `primaryNav`'s content into the top bar: `header.topbar` carries,
  left to right, the brand (now `<a href="/">` around the existing mark and
  wordmark), the global destinations, and `.top-right` (§4's inbox icon
  arrives in Task 6; theme toggle and avatar unchanged). The separate
  `nav.global` row below the bar goes away as a visual row; keep exactly
  one Primary nav landmark (`<nav aria-label="Primary">`) inside the bar —
  the landmark, its `id="global-nav"`, and the More toggle's
  aria-expanded/controls wiring survive so `nav.js` needs only selector
  adjustments, if any.
- Trim the destination list to the five in Global Constraints, in order.
  Delete the `home`, `reviews`, `deliveries` cases from `globalIcon` and
  their `globalLink` lines. Update `primaryNav`'s doc comment: it now
  states 056 §1's list and cites the amendment, and stops citing 032 §2's
  eight.
- Callers passing `ActiveGlobal: "home" | "reviews" | "deliveries"`
  (`render.go`, `web.go`) now pass those pages' nav key as `""` — the page
  is no longer a destination, so its global nav marks nothing and the page
  keeps its single `aria-current` elsewhere or none (Home's brand link is
  not a nav item). Sweep `grep -rn 'ActiveGlobal: "\(home\|reviews\|deliveries\)"' internal/`.
- Stylesheet: the `.topbar` grows the nav between brand and `.top-right`;
  the below-880px behaviour carries over in its new position (primary tabs
  + More, secondaries expanding below the bar). Adjust only what the move
  requires.
- Tests: `TestGlobalNavOrder` (and any sibling asserting the eight-name
  order) is re-pinned to the five names in order; the one-aria-current
  invariant still holds on every page it covers; the More-toggle active
  expression test (if present) covers `intake`/`knowledge`. Handler tests
  asserting `>Home<`/`>Reviews<`/`>Deliveries<` markers in the nav are
  updated — the routes themselves still serve their pages, so page-content
  assertions stay.
- `docs/follow-ups.md`: rewrite the WL-238 entry ("eight destinations …")
  — the owed 032 §2 amendment is now spec 056 §1, landed; what remains owed
  there, if anything, shrinks accordingly.

- [ ] Re-pin the nav tests to the five-destination order; watch them fail
- [ ] Reshape `layout.templ` + stylesheet; `go generate ./...`
- [ ] `go test -trimpath ./internal/ui ./internal/api -count=1` → ok
- [ ] Manual check at 1440px and 375px against the compose stack: one row,
      brand links to `/`, More expands Intake and Knowledge
- [ ] Commit including generated artifacts

### Task 2 — The task page carries the project sidebar

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [1]
```

`internal/api/web.go` `taskPage` (:447) and `internal/ui/task.templ`:

- The handler adds one lookup: the task's project identity via the same
  call `projectHeader`/`GET /projects/{id}` already makes (`GetProject` —
  id, name, key). Thread a `CockpitProject` into the task view.
- `task.templ` renders through the project shell (`projectShell` /
  `sidebar` — the exact frame `crew.templ` uses) with local-nav `active`
  `""`: none of the sidebar destinations is current (§2), and the global
  nav also gets `""`.
- The invariant: per the Decisions block, `assertOneAriaCurrent` learns the
  one page allowed zero, named explicitly.

Tests, in `internal/api/web_test.go`: the task page contains the project
sidebar's nav landmark and the project's name linking to its Overview; it
renders zero `aria-current` (the named exception); an unknown task still
404s; every other page still renders exactly one.

- [ ] Write the sidebar assertions; watch them fail
- [ ] Handler + templ change; `go generate ./...`
- [ ] `go test -trimpath ./internal/api -count=1` → ok
- [ ] Commit including generated artifacts

### Task 3 — Pure inbox assembly: buckets, order, membership filter

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New `internal/api/inbox.go` (the page assembly; unrelated to spec 020's
`inbox_mirror.go` — say so in the file comment) with the pure heart:

```go
// inboxInputs is everything the inbox derivation reads, already fetched.
// Reviews are every open pr-kind approval org-wide with its project, PR
// title/URL, and author login; Facts are the all-projects work facts —
// fed whole so det-v1 resolves blocked-by chains across project
// boundaries (056 §3.3) — and Membership/Led come from ProjectsForActor.
type inboxInputs struct {
	ActorID     string
	ActorLogin  string // the actor's GitHub login, "" when none stored
	Reviews     []store.InboxReview
	Facts       []store.ProjectWorkFact
	Membership  map[string]bool // project id -> member
	Led         map[string]bool // project id -> is_lead
	Now         time.Time
}

// assembleInbox derives 056 §3.2's six buckets in order. Work buckets rank
// by rankSecondaryConcerns over the full facts slice, filtered to
// membership afterwards — never before (§3.3). nil when every bucket is
// empty. Each item carries the text and href the page renders.
func assembleInbox(in inboxInputs) *ui.InboxView
```

`ui.InboxView`/`ui.InboxBucket`/`ui.InboxItem` land in `internal/ui/views.go`
as plain structs (Task 5 renders them): bucket label + items (text, href,
muted detail — project key, age). Classification per §3.1/§3.2:

- review *assigned*: `RequiredActor == ActorID`;
- review *unassigned-in-led*: `RequiredActor == nil` and `Led[project]`;
- review *owned*: author login equals `ActorLogin` case-insensitively and
  someone else is required;
- work *assigned*: active-state task with `Assignee == ActorID`; *owned*:
  `CreatedBy == ActorID` and assignee ≠ actor; *near*: any other
  active-state task in a member project.
- Work-bucket internal order: the position of the task's root-cause concern
  in `rankSecondaryConcerns(Facts, Now)` filtered to concerns whose held
  tasks intersect membership; tasks no concern holds follow, by priority
  rank then id (a deterministic tail — det-v1 only ranks blocked roots).

First test, `internal/api/inbox_test.go`, table-driven, no database. Pin at
minimum: the six buckets appear in §3.2's order with the exact headings;
lead-only bucket 2 (a non-lead member never sees it); the §3.3 order — a
cross-project blocked-by chain whose root lives outside the actor's
membership still ranks the member-project concern by the true root (build
the fixture the spec describes: filtering first would name the wrong task,
and the test fails if assembly filters before ranking); review buckets
oldest-first with the id tiebreak; case-insensitive authorship; nil on
empty.

- [ ] Write the tests; watch them fail to compile
- [ ] Implement `assembleInbox` + the ui structs
- [ ] `go test -trimpath ./internal/api -run TestAssembleInbox -count=1` → ok
- [ ] Commit

### Task 4 — Store readers: open PR reviews org-wide, and the existence check

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

In `internal/store/approvals.go` (the approvals fact family):

```go
// InboxReview is one open pr-kind approval as the cross-project inbox
// consumes it (spec 056 §3.1).
type InboxReview struct {
	ApprovalID    int64
	Project       string
	EntityID      string // repo#number
	Title, URL    string
	AuthorLogin   string
	RequiredActor *string
	RequiredRole  *string
	CreatedAt     time.Time
}

// ListInboxReviews returns every open ('awaiting' | 'changes_requested')
// pr-kind approval across all projects, oldest first, id tiebreak — the
// membership scoping happens in the pure assembly (056 §3.3's
// score-wide-filter-late rule applied uniformly).
func (s *Store) ListInboxReviews(ctx context.Context) ([]InboxReview, error)

// HasInboxItems answers 056 §4's indicator: does at least one inbox item
// exist for the actor. One statement of EXISTS branches (assigned review;
// unassigned review in a led project; owned review by login; active task
// assigned/created; active task in a member project) that stops at the
// first hit — never the full ranked query.
func (s *Store) HasInboxItems(ctx context.Context, actorID string) (bool, error)
```

`ListInboxReviews` is `prEntityJoin` → `tasks` → `projects` (the queue
reader's join) plus the PR's author column; no membership predicate.
`HasInboxItems` embeds the membership predicate in SQL (join
`project_participants`) because it must not fetch; keep each branch's
predicate textually beside a comment naming its §3.2 bucket so the two
readers cannot silently diverge.

Tests (Postgres): seed the part-1 approvals fixtures across two projects;
`ListInboxReviews` returns both projects' rows oldest-first with author
logins; `HasInboxItems` is true for an actor with only an assigned review,
true for a lead with only an unassigned review in their led project, false
for a non-member with nothing, false for an empty database; and true for an
actor whose only item is an active task they created.

- [ ] Write the tests; watch them fail
- [ ] Implement both readers
- [ ] `go test -trimpath ./internal/store -run 'TestListInboxReviews|TestHasInboxItems' -count=1`
      → ok (not SKIP)
- [ ] Commit

### Task 5 — Tracer: `GET /inbox`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [3, 4]
```

- `internal/api/router.go`: `"GET /inbox": guarded(permWebRead)` — the same
  guard `/work` has; no new permission (§3).
- Handler `inboxPage` in `internal/api/inbox.go`: `subjectFrom(r)`; with no
  actor (open mode) render the page with the honest signed-out empty state
  (no items, no fabrication). Otherwise fetch: `GetActor` (login),
  `ProjectsForActor` (membership + led), `ListInboxReviews`,
  `ListProjectWorkFacts(ctx, "")`; `assembleInbox`; render
  `ui.Inbox(view)`. Record `worklode_web_inbox_renders_total{outcome}` —
  `empty` when the view is nil, else `rendered` — a nil-safe instrument in
  `internal/api/metrics.go` following `worklode_web_home_renders_total`.
- `internal/ui/inbox.templ`: the global shell, heading `Inbox`, one
  `<section class="card">` per non-empty bucket in order (empty buckets are
  omitted — honest empty states), items as worklist rows linking their
  href; the nil view renders `Nothing is waiting on you.` Nav key `""`.

Tests in `internal/api` (Postgres, the `newOIDCServer` + `webLogin`
session harness): seed the §3.2 spread — an assigned review, an unassigned
review in a led project, an owned PR, an assigned task, a created task, a
neighbour task — GET `/inbox` and assert all six headings in order and each
item's link; a non-lead second actor sees no bucket-2 heading; the
signed-out stack (`newTestServer`) gets 200 with the empty line; metric
outcomes.

- [ ] Write the tests; watch them fail (route 404s)
- [ ] Route, handler, templ, metric; `go generate ./...`
- [ ] `go test -trimpath ./internal/api -count=1` → ok (boot check proves
      the guard row)
- [ ] Commit including generated artifacts

### Task 6 — The inbox indicator on every page

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [1, 4]
```

- `internal/ui/layout.templ`: the top bar gains, immediately left of the
  theme toggle, an `iconbtn` anchor to `/inbox` with `aria-label="Inbox"`,
  an inline bell-style SVG in the topbar's icon style, and — only when the
  context flag is set — the alert-toned dot (`dot-alert`, a new variant of
  the shell's existing dot treatment in `app.tailwind.css`; the unreached
  count-badge styling stays for the future numeric badge, §4).
- `internal/ui/context.go` (new, stdlib-only): a typed context key,
  `WithInboxDot(ctx, bool)` and the package-internal read `Page` uses.
- `internal/api/webform.go` `renderWeb`: before rendering, when
  `subjectFrom(r)` names an actor, call `HasInboxItems` once and wrap the
  render context with `ui.WithInboxDot`. On a store error, log and render
  without a dot — the indicator must never fail a page. No other call site
  computes it (§4's once-per-request rule, structurally).

Tests: a `ui` render test pins the icon renders on every page and the dot
only with the flag; an `internal/api` test asserts a logged-in actor with
one awaiting review sees the dot on `/projects` (a page that knows nothing
about inboxes) and loses it once the review is decided; the signed-out
stack renders the icon without a dot; `HasInboxItems` is called once per
request (assertable by counting store queries the way existing
request-count tests do, or by a wrapped-store counter — follow the
`TestDocTodoClosureCostsOneRequest` precedent).

- [ ] Tests first; watch them fail
- [ ] Implement; `go generate ./...`
- [ ] `go test -trimpath ./internal/ui ./internal/api -count=1` → ok
- [ ] Commit including generated artifacts

### Task 7 — e2e and docs alignment

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [2, 5, 6]
```

- `e2e/smoke_test.go` `assertWebPages`: `GET /inbox` → 200 with the
  `Inbox` heading; the top bar of any page carries the inbox anchor; the
  global nav lists exactly the five destinations (assert `>Ideas<` …
  `>Knowledge<` present and `>Home<`/`>Reviews<`/`>Deliveries<` absent
  from the Primary nav landmark); `/reviews` and `/deliveries` still
  return 200 by URL (§1: routes unchanged).
- Comment sweep adjacent to the change: `layout.templ`'s header comment
  describes the one-row shell; `web.go`'s taskPage comment mentions the
  sidebar; nothing still describes the two-row nav
  (`grep -rn "navigation row" internal/ui internal/api`).
- `docs/follow-ups.md`: verify Task 1's WL-238 rewrite against what
  actually merged; add nothing this plan's `covers:` already declares.

- [ ] Extend `assertWebPages`;
      `go test -trimpath -race -count=1 -tags e2e ./e2e/ -run TestSmoke` → ok
- [ ] Comment sweep; `go test -trimpath ./...` → green
- [ ] Commit
