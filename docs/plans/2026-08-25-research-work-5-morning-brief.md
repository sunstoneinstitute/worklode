---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.2
    coverage: full
  - spec: docs/specs/032-project-cockpit.md#sec-9
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-home-project-list.md
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
requires:
  - docs/plans/2026-08-14-home-project-list.md
---
# Research work part 5 — the Morning Brief

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Worklode's first human-facing consequence exists: the per-user
Morning Brief on Home (029 §8.2, 032 §9). A signed-in actor sees, above the
project cards `2026-08-14-home-project-list` built, what governed events
since their previous review mean for them — decisions and exceptions first,
routine successful work collapsed to a count — grouped by project. Opening
Home changes nothing; an explicit **Reviewed through now** action advances
the actor's event boundary to the displayed cutoff, and unresolved decisions
survive that advance because they are derived from open state, not from the
event window.

**Architecture:** one new table, no new subscriber, no new notifier. The
brief is a *pull*: migration 0063 adds `actor_event_cursor` (the per-actor
event boundary), and `homePage` reads events with id above the boundary
through the existing horizon-bounded `store.ListEvents`, feeds them plus the
already-fetched approval/membership state to a pure derivation
(`assembleMorningBrief` in `internal/api/morningbrief.go`), and renders the
result with a new `internal/ui/morningbrief.templ` section inside the Home
page. `POST /home/reviewed` is the one mutation: a session-gated web form
that advances the cursor forward-only to the cutoff the page displayed.
The layer split copies `2026-08-14-home-project-list`: pure classification,
then pure assembly, both table-tested with no database, then store, then the
tracer, then the mutation.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §8.2
- `docs/specs/032-project-cockpit.md` §9, §11
- `docs/plans/2026-08-14-home-project-list.md` — the shapes this plan
  extends: `homeInputs`/`homeCardFacts`/`homeCards` (`internal/api/home.go`),
  `homePage` (`internal/api/web.go:190`), `ui.HomeView`
  (`internal/ui/views.go:753`), `internal/ui/home.templ`
- `internal/store/events.go` — `Event`, `ListEvents`/`EventFilter` (`:494`),
  `MaxEventListLimit`, `EventLogHorizonID` (`:697`), the `eventHorizon`
  predicate and why every cursor read carries it
- `internal/eventbus/loop.go` and `internal/api/docwatch.go` — the 025 §15
  subscriber machinery this plan deliberately does *not* extend
- `internal/api/webform.go:385` — `decideApproval`, the session-gated web
  mutation pattern (`sameOriginForm`, `parseWebForm`, 303 redirect)
- `internal/api/router.go` — `routeGuards`; `internal/api/server.go:560` —
  how `requireSession` wraps a route at registration
- `internal/store/participants.go:239` — `OpenWorkOwnedBy`
- `deploy/base/migrations/0021_event_log.up.sql` — `event_subscribers`, the
  precedent `actor_event_cursor` follows

## Coverage: why each level is what it is

- **029 §8.2 `full`** — every §8.2 obligation is either delivered here or
  already standing: the offset-tracked subscriber machinery predates this
  plan (see Global Constraints), no producing handler gains a notifier, the
  MVP sends nothing outbound, and the Morning Brief with its explicit
  boundary is what this plan builds. §8.4's crew-space subscriber is §8.4's
  own scope (part 7 of this series), named by §8.2 only as the exception
  that proves the rule.
- **032 §9 `partial`, full with `2026-08-14-home-project-list`** — that plan
  delivered the project list and card tiers; this one delivers the rest: the
  brief (grouping, ordering, collapse, persistence), the event boundary, and
  **Reviewed through now**. §9's "assigned human work" and "approvals" enter
  the brief's needs-you tier from open state (`OpenWorkOwnedBy`,
  `ApprovalsAwaiting`); agent work the actor supervises surfaces as the
  stopped-runs tier and the collapsed routine count, which is the §11
  acceptance framing of that sentence — judgment obvious, no firehose.
- **032 §10 `none`** — binding, not delivered: the brief section reflows at
  narrow widths, its one disclosure control is a real `<details>`, and the
  review action is a labelled form button, but this plan adds no
  accessibility scope beyond the surface it builds.
- **032 §11 `none`** — the standing rule: e2e drives public surfaces only,
  never a direct store write. The three-slice release demonstration is not
  planned here.

## Global Constraints

- **025's offset-tracked subscribers come before any outbound consequence,
  and they already exist.** The binding order of 029 §8.2 is satisfied by
  the machinery in `internal/eventbus/` (`loop.go`, `emit.go`, `vocab.go`,
  `metrics.go`), started from `internal/api/server.go:944` only when the
  caller passes a `BackgroundCtx`, operated via `lode event subscribers` and
  `lode event seek` (`internal/cmd/event.go:116,144`), with `doc-lifecycle`
  (`internal/api/docwatch.go:56`) as the one registered subscriber. **No
  producing handler gains a hardcoded notifier in this plan, and no task
  below registers a subscriber** — the brief is derived when the user
  returns, so `actor_event_cursor` is a read boundary like the
  `event_subscribers` offsets, not a consumer.
- **The MVP sends nothing.** No scheduled email, no ad-hoc Google Chat
  message, no off-hours notification for work Worklode orchestrates;
  production alerts remain a separate system. Worklode's first human-facing
  consequence is this per-user Morning Brief in the web UI, derived from
  lifecycle events when the user returns. No task below builds an outbound
  channel, and a reviewer should reject one that tries.
- **Ordering is fixed by 032 §9, quoted exactly.** The brief groups by
  project or pinned focus and orders content as follows:
  1. decisions and exceptions needing the actor;
  2. material outcomes and changes;
  3. runs that stopped or reached a bound; and
  4. routine successful work, collapsed.
  Decisions and exceptions persist; routine successful activity collapses.
  In this plan "grouped by pinned focus" renders as the project group's
  pinned-focus note (`store.Project.FocusNote`) in its heading — `Focus` is
  the ranking-concern list, not a grouping key.
- **Opening Home never advances the boundary.** `GET /` is read-only against
  `actor_event_cursor`. Only `POST /home/reviewed` advances it, to the
  cutoff the page displayed (a hidden form field), forward-only, clamped to
  the event-log horizon. After an advance, unresolved decisions remain in
  subsequent briefs (tier 1 is derived from open state, not events); routine
  updates at or before the cutoff collapse out entirely.
- **The persistence split is structural.** Tier 1 comes from current open
  state (`ApprovalsAwaiting`, `OpenWorkOwnedBy`) and ignores the cursor;
  tiers 2–4 come only from events with `id > last_event_id`. Never derive
  tier 1 from the event window — that is what would make a reviewed decision
  vanish while still undecided.
- **Name collision, declared:** `internal/api/brief.go` and
  `internal/store/brief.go` are the **task start-of-work brief**
  (`GET /api/v1/tasks/{id}/brief`) and are unrelated to this plan. An
  implementer grepping for "brief" lands there first. Everything this plan
  adds uses the `morningBrief`/`MorningBrief` prefix and lives in
  `morningbrief*.go` / `morningbrief.templ` / `actorcursor.go` — never a
  bare `brief` identifier or filename.
- **Migration 0063 is this plan's one migration**, and its shape is fixed
  across the 029 series:
  `actor_event_cursor (actor_id text PK, last_event_id bigint, updated_at
  timestamptz)`. A new `.up.sql`/`.down.sql` pair listed in
  `deploy/base/kustomization.yaml`; never edit a shipped migration;
  `./scripts/check-migrations.sh --no-fix` before committing.
- **Advancing the cursor is a plain row write, not an event.** The precedent
  is `event_subscribers`: read offsets over the log are bookkeeping, not
  facts about work. Recording an event per review would also feed the brief
  its own reviews.
- **Tier classification is by event type, pinned here.** Tier 3 (stopped or
  reached a bound): `task.stopped`, `task.abandoned`, `lease.expired`,
  `runtime.crashloop`, `runtime.oom`, `runtime.flux_failure`. Tier 2
  (material outcomes and changes): `wl:DocumentSubmitted`,
  `wl:DocumentAccepted`, `task.done`, `task.reopened`, `deliverable.created`,
  `approval.decided`, `crew.member_added`, `crew.member_removed`,
  `issue.promoted`, `runtime.flux_recovery`. Everything else is tier 4,
  routine, collapsed to a count. The map is one `switch` in one pure
  function; extending it later is a one-line diff plus a table row.
- **Project attribution is pure and ordered:** payload key `project`; else
  the `<KEY>` prefix of payload key `task` resolved through the projects'
  keys; else `repository.full_name` resolved through the project-repo
  mapping. An event that resolves to no project is excluded from the brief
  (it is exactly the firehose §11 forbids). Brief scope is the actor's
  projects: membership union projects with approvals awaiting them.
- **Exact spellings.** Section heading: `Morning Brief`. Collapsed routine
  line: `1 routine update` / `N routine updates`. Truncation line:
  `Showing the oldest N events since your last review.` Empty-but-advanced
  state: `Nothing needing you since your last review.` Button label:
  `Reviewed through now`. Route: `POST /home/reviewed`. Form field:
  `cutoff`. Store methods: `ActorEventCursor`, `AdvanceActorEventCursor`.
- **Metrics** (spec 022): two new nil-safe instruments in
  `internal/api/metrics.go` —
  `worklode_web_morning_brief_renders_total{outcome}` with outcome bounded
  to `rendered` | `empty` | `truncated`, and
  `worklode_web_brief_reviews_total{outcome}` with outcome bounded to
  `advanced` | `noop` | `invalid`. Never a project, task, or actor id in a
  label.
- **Route guarding:** `POST /home/reviewed` gets a `routeGuards` row
  (`guarded(permWebWrite)`) and is registered wrapped in `requireSession`,
  exactly as `POST /approvals/{id}/decide` is (`server.go:560`) — a bearer
  token has no browser session and an open instance has no actor to keep a
  cursor for. `NewServer` refuses to boot on an unguarded route; do not work
  around the table.
- **ADR 036:** nothing here crosses the `/api/v1` wire, so no `internal/model`
  type is added. The new view types are `internal/ui` package-locals; the
  cursor methods scan into plain `int64`. If a later part wants the brief as
  JSON, that is when a model type (and a 036 conversation) happens.
- **Store and `internal/api` tests need Postgres with pgvector**
  (`TEST_POSTGRES_DSN`); they skip without it and a skipped run proved
  nothing. Pure tests (tasks 1–2) and render tests (task 4) must not touch a
  database. `e2e/` drives public surfaces only — never a direct store write.
- **Every task leaves `go test -trimpath ./...` green** and ends in its own
  commit. Never bare `go test`/`go build` (CLAUDE.md). Commit messages
  describe the change, never the plan file, and carry no `Co-authored-by:`.

---

## Tasks

### Task 1 — Pure classification: tier and project attribution per event

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New file `internal/api/morningbrief.go` (see the collision note in Global
Constraints — this is not `brief.go`). Two pure functions and the tier type,
no store, no HTTP:

```go
// morningBriefTier is 032 §9's ordering, from the spec's own numbered list.
// Tier 1 (decisions and exceptions needing the actor) is never assigned to
// an event: it is derived from open state in assembleMorningBrief, which is
// what makes it persist across cursor advances.
type morningBriefTier int

const (
	briefTierOutcome morningBriefTier = 2 // material outcomes and changes
	briefTierStopped morningBriefTier = 3 // runs that stopped or reached a bound
	briefTierRoutine morningBriefTier = 4 // routine successful work, collapsed
)

// morningBriefTierOf classifies one event type per the pinned table in the
// plan's Global Constraints. Unknown types are routine — the default that
// keeps a new producer from paging a human by accident.
func morningBriefTierOf(eventType string) morningBriefTier

// morningBriefProject attributes one event to a project: payload "project",
// else the "<KEY>-" prefix of payload "task" via keyToProject, else the
// payload's repository.full_name via repoToProject. "" means unattributed —
// the caller drops the event from the brief.
func morningBriefProject(ev store.Event, keyToProject, repoToProject map[string]string) string
```

`morningBriefProject` unmarshals `ev.Payload` once into `map[string]any`;
a payload that is not an object attributes to `""`. The task prefix is
`taskID[:strings.IndexByte(taskID, '-')]` guarded against a missing dash;
`repository.full_name` is the nested GitHub-webhook shape
(`payload["repository"].(map[string]any)["full_name"]`).

First test, new `internal/api/morningbrief_test.go`, table-driven, no
database:

```go
func TestMorningBriefTierOf(t *testing.T) {
	cases := map[string]morningBriefTier{
		"task.stopped":         briefTierStopped,
		"lease.expired":        briefTierStopped,
		"runtime.crashloop":    briefTierStopped,
		"wl:DocumentAccepted":  briefTierOutcome,
		"task.done":            briefTierOutcome,
		"approval.decided":     briefTierOutcome,
		"crew.member_added":    briefTierOutcome,
		"task.created":         briefTierRoutine, // default
		"push":                 briefTierRoutine,
		"never.seen.before":    briefTierRoutine, // unknown = routine
	}
	// one subtest per entry, plus entries for every type the Global
	// Constraints table pins — the test is the table's mirror.
}
```

`TestMorningBriefProject` pins the attribution order: payload `project`
wins over `task`; `task: "WL-7"` resolves via `{"WL": "proj"}`; a GitHub
payload with `repository.full_name` resolves via the repo map; a payload
with none of the three, a non-object payload, and an unknown key each
return `""`.

- [ ] Write both tests; watch them fail to compile
- [ ] Implement the tier type and both functions in
      `internal/api/morningbrief.go`
- [ ] `go test -trimpath ./internal/api -run TestMorningBrief -count=1` →
      `ok  github.com/sunstoneinstitute/worklode/internal/api`
- [ ] Commit: `git add internal/api && git commit` — subject e.g.
      `Classify events into Morning Brief tiers`

### Task 2 — Pure assembly: groups, order, collapse, cutoff

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Still `internal/api/morningbrief.go`. The derivation is the heart of the
part: everything already fetched goes in, one renderable view comes out, and
the whole 032 §9 contract is testable with plain structs.

```go
// morningBriefInputs is everything the brief derivation reads, already
// fetched. Events are ascending by id, all above Boundary; Truncated means
// the fetch hit its cap and Events is a prefix. Order is the project-id
// display order (the Home cards' order); Awaiting and Assigned are the
// tier-1 open state, keyed by project id, and are independent of Events.
type morningBriefInputs struct {
	Events        []store.Event
	Truncated     bool
	Boundary      int64 // the stored cursor (0 = never reviewed)
	Order         []string
	Projects      map[string]store.Project // by id, for Name and FocusNote
	Awaiting      map[string]int           // ApprovalsAwaiting counts
	Assigned      map[string][]store.OwnedWork
	KeyToProject  map[string]string
	RepoToProject map[string]string
}

// assembleMorningBrief derives the brief. nil when there is nothing at all:
// no tier-1 state anywhere and no events past the boundary — the section
// then does not render. Groups appear in Order; a project with neither
// tier-1 state nor attributed events gets no group. Within a group: tier 1
// from Awaiting/Assigned, tiers 2 and 3 from events in id order, tier 4 as
// a count. Cutoff is the highest event id seen (Boundary when Events is
// empty); CanReview is Cutoff > Boundary.
func assembleMorningBrief(in morningBriefInputs) *ui.MorningBriefView
```

Item text is a small pure formatter in the same file
(`morningBriefItemText(ev) (text, href string)`): a `task` payload key
yields `<type label>: <task id>` linking `/tasks/<id>`; `approval.decided`
and awaiting counts link `/reviews`; the fallback is the bare type label
with no link. Tier-1 items: `N approvals awaiting you` (singular per the
Global Constraints spelling, href `/reviews`) and one
`Assigned to you: <id> <title>` per open `OwnedWork` row (href
`/tasks/<id>`). Keep the formatter dumb — the acceptance bar is legibility,
not payload archaeology.

The `ui.MorningBriefView` / `ui.MorningBriefGroup` / `ui.MorningBriefItem`
types land in task 4; to keep this task self-contained, add them to
`internal/ui/views.go` here as plain structs (fields only, no templ), and
task 4 renders them:

```go
// --- morning brief (032 §9; NOT the task brief in internal/api/brief.go) --

type MorningBriefView struct {
	Cutoff    int64 // displayed cutoff; the hidden form value
	CanReview bool  // Cutoff advanced past the stored boundary
	Truncated bool
	Shown     int // events represented, for the truncation line
	Groups    []MorningBriefGroup
}

type MorningBriefGroup struct {
	ProjectID, Name string
	FocusNote       string // pinned focus, "" = none
	NeedsYou        []MorningBriefItem // tier 1: decisions and exceptions
	Outcomes        []MorningBriefItem // tier 2: material outcomes and changes
	Stopped         []MorningBriefItem // tier 3: stopped or reached a bound
	Routine         int                // tier 4: routine, collapsed to a count
}

type MorningBriefItem struct {
	Text string
	Href string // "" renders as plain text
}
```

First test, `TestAssembleMorningBrief` in
`internal/api/morningbrief_test.go`, table-driven, no database. Pin at
minimum:

- **the §9 order**: a group with all four tiers renders NeedsYou, then
  Outcomes, then Stopped, then the Routine count — assert field placement,
  not string order;
- **collapse**: five `task.created`/`push` events → `Routine: 5`, no items;
- **persistence**: `Awaiting` and `Assigned` populate NeedsYou even with
  `Events` empty (`CanReview` false, `Cutoff == Boundary`) — this is the
  "unresolved decisions remain after the cursor advances" test, stated as
  state-not-events;
- **scope**: an event attributed to a project absent from `Order` is
  dropped; an unattributed event is dropped but still counted in `Shown`
  and still moves `Cutoff`;
- **cutoff**: `Cutoff` = max event id; empty events → `Boundary`;
  `Truncated` passes through;
- **nil**: no state, no events → nil;
- **empty-but-advanced**: events exist but all drop → non-nil view,
  `CanReview` true, zero groups (task 4 renders the
  `Nothing needing you since your last review.` line from exactly this
  shape).

- [ ] Write `TestAssembleMorningBrief` (subtests per bullet); watch it fail
- [ ] Add the ui view structs; implement `assembleMorningBrief` and
      `morningBriefItemText`
- [ ] `go test -trimpath ./internal/api -run TestAssembleMorningBrief -count=1`
      → `ok`
- [ ] `go test -trimpath ./internal/ui -count=1` → `ok` (structs compile)
- [ ] Commit: e.g. `Derive the Morning Brief from events and open state`

### Task 3 — Migration 0063: `actor_event_cursor`, and the cursor store methods

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - golang-migrate:migration
blockedBy: [ ]
```

`deploy/base/migrations/0063_actor_event_cursor.up.sql` — the shape is
fixed across the 029 series, do not redesign it:

```sql
-- Per-actor Morning Brief boundary (029 §8.2, 032 §9). A read cursor over
-- the events log, like event_subscribers' offsets: bookkeeping, not a fact,
-- so writes to it are plain row writes, not events.
CREATE TABLE actor_event_cursor (
    actor_id      text PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    last_event_id bigint NOT NULL CHECK (last_event_id >= 0),
    updated_at    timestamptz NOT NULL
);
```

(`ON DELETE CASCADE`, unlike the baseline's RESTRICT default: the cursor is
a per-user convenience with nothing referencing it, and an actor deletion
must not be blocked by their read position.) `0063_actor_event_cursor.down.sql`
drops the table. List both files in `deploy/base/kustomization.yaml` after
the 0062 pair (part 6 claims 0061–0062 in parallel; `check-migrations.sh`
renumbers on collision, so the number is nominal until this lands).

New file `internal/store/actorcursor.go` (not `brief.go` — that name is
taken by the task brief):

```go
// ActorEventCursor returns the actor's Morning Brief boundary: the highest
// event id they have explicitly reviewed through. 0 when the actor has
// never reviewed — the brief then starts from the beginning of the log.
func (s *Store) ActorEventCursor(ctx context.Context, actorID string) (int64, error)

// AdvanceActorEventCursor moves the boundary forward to `to`, upserting the
// row. Forward-only: a `to` at or below the stored value is a no-op
// (advanced=false), never a rewind — GREATEST in the upsert, not a check in
// the handler.
func (s *Store) AdvanceActorEventCursor(ctx context.Context, actorID string, to int64) (advanced bool, err error)
```

The upsert is one statement:
`INSERT ... ON CONFLICT (actor_id) DO UPDATE SET last_event_id =
GREATEST(actor_event_cursor.last_event_id, EXCLUDED.last_event_id),
updated_at = EXCLUDED.updated_at` with `advanced` read back via
`RETURNING last_event_id = $2`. Reject `to <= 0` and an empty `actorID`
with `ErrInvalidInput`.

First test, `internal/store/actorcursor_test.go` (real Postgres via
`openTestStore(t)`; skips without it — run with Postgres up or the task
proved nothing): seed an actor; `ActorEventCursor` → 0; advance to 40 →
`advanced=true`, read 40; advance to 25 → `advanced=false`, read still 40;
advance to 41 → true; unknown actor id → FK error surfaces; `to=0` →
`ErrInvalidInput`.

- [ ] Write the migration pair; `./scripts/check-migrations.sh --no-fix` →
      no collisions reported
- [ ] Add both files to `deploy/base/kustomization.yaml`
- [ ] Write `TestActorEventCursor`; watch it fail
- [ ] Implement `internal/store/actorcursor.go`
- [ ] `go test -trimpath ./internal/store -run TestActorEventCursor -count=1`
      → `ok  github.com/sunstoneinstitute/worklode/internal/store` (not SKIP)
- [ ] `go test -trimpath ./...` → green
- [ ] Commit: e.g. `Add the per-actor event boundary (actor_event_cursor)`

### Task 4 — The brief section: templ component and stylesheet

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [2]
```

New `internal/ui/morningbrief.templ`, rendered from `home.templ`. Add
`Brief *MorningBriefView` to `ui.HomeView` (nil = no section — open mode,
or nothing to say) and insert `@morningBrief(v.Brief)` between the `<h1>`
and the card grid, inside the actor-mode branch only. Shape:

```
templ morningBrief(b *MorningBriefView) {
	if b != nil {
		<section class="morningbrief" aria-label="Morning Brief">
			<h2>Morning Brief</h2>
			if b.Truncated {
				<p class="small muted">Showing the oldest { b.Shown } events since your last review.</p>
			}
			if len(b.Groups) == 0 {
				<p class="muted">Nothing needing you since your last review.</p>
			}
			for _, g := range b.Groups { @morningBriefGroup(g) }
			if b.CanReview {
				<form method="post" action="/home/reviewed">
					<input type="hidden" name="cutoff" value={ strconv.FormatInt(b.Cutoff, 10) }/>
					<button type="submit">Reviewed through now</button>
				</form>
			}
		</section>
	}
}
```

`morningBriefGroup(g)`: an `<h3>` linking `/projects/{g.ProjectID}` with the
name plus, when `FocusNote != ""`, a `.small.muted` pinned-focus span; then
the three item lists in tier order (each item an `<a>` when `Href != ""`,
plain text otherwise — NeedsYou items first and visually strongest); then,
when `Routine > 0`, one `.muted` line with the exact collapsed spelling
(`1 routine update` / `N routine updates`) inside a `<details>` whose
summary is that line and whose body says the updates are at or before the
cutoff and collapse on review — no per-event routine rendering anywhere.
That absence is the §11 acceptance bar in markup form: judgment obvious,
no firehose.

Styles: a new `/* ---------- morning brief ---------- */` section in
`internal/ui/styles/app.tailwind.css` on the existing custom properties
(`--surface`, `--line`, `--ink`); reuse `.chip`, `.muted`, `.small`. Point
at the stylesheet — do not transcribe it here. The section is a single
column and needs no width rules beyond what the shell provides.

First test, `internal/ui/views_test.go`: render `Home` with a fixture
`HomeView` whose `Brief` has one group carrying all four tiers, and assert:
`Morning Brief` heading present; the NeedsYou item text appears before the
Outcomes text, which appears before the Stopped text (index comparison on
the rendered buffer); the routine count renders as `3 routine updates` and
no routine item text appears; the form posts to `/home/reviewed` with
`name="cutoff"` and the button label `Reviewed through now`. Second
subtest: `Brief: nil` renders no `morningbrief` section; third:
`CanReview: false` renders no form; fourth: zero groups with
`CanReview: true` renders the nothing-needing-you line and the form.

- [ ] Write the render tests; watch them fail to compile
- [ ] Write `morningbrief.templ`, extend `HomeView` and `home.templ`, add
      the stylesheet section
- [ ] `go generate ./...` — `git status` shows `morningbrief_templ.go`,
      `home_templ.go`, and `internal/ui/assets/app.css` regenerated
- [ ] `go test -trimpath ./internal/ui -count=1` → `ok`
- [ ] Commit **including the generated artifacts**: e.g.
      `Render the Morning Brief section on Home`

### Task 5 — Event-window fetch and the two metrics instruments

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

The brief needs every event above the boundary, and `store.ListEvents`
caps a page at `store.MaxEventListLimit` (200). Reuse it — no new store
reader; `internal/api/eventstream.go`'s `streamHead` is the paging
precedent. In `internal/api/morningbrief.go`:

```go
// morningBriefEventCap bounds one brief render.
// ponytail: flat cap, oldest-first; an actor away long enough to accrue
// more sees a truncation line and reviews forward in steps. Upgrade path:
// summarize-then-page, only if real briefs ever hit the cap.
const morningBriefEventCap = 2000

// briefEventsSince pages ListEvents (After cursor) until a short page, the
// cap, or the horizon. Ascending id order, horizon-bounded by ListEvents
// itself. truncated reports hitting the cap with more behind it.
func (s *server) briefEventsSince(ctx context.Context, after int64) (events []store.Event, truncated bool, err error)
```

Metrics, in `internal/api/metrics.go`, following `observeHomeRender`'s
pattern exactly (nil-safe method, bounded label values as package consts,
registered in `initMetrics`):

- `worklode_web_morning_brief_renders_total{outcome}` — `rendered` |
  `empty` | `truncated`; `observeMorningBriefRender(outcome string)`.
- `worklode_web_brief_reviews_total{outcome}` — `advanced` | `noop` |
  `invalid`; `observeBriefReview(outcome string)`.

Tests: `TestBriefEventsSince` in `internal/api` (Postgres): seed 250+
events through `store.RecordEvent` in the test store (test seeding, not a
public-surface rule — that rule binds `e2e/` only), then assert a fetch
from 0 crosses the 200-row page boundary and returns all in ascending
order; a fetch with `after` mid-log returns only the tail; drop the cap to
a small value via a test-only const override if needed — otherwise assert
`truncated` stays false. Metrics get a registry test alongside
`metrics_internal_test.go`'s existing cases (bounded label sets, nil-safe
on a bare `*server`).

- [ ] Write `TestBriefEventsSince` + the metrics tests; watch them fail
- [ ] Implement `briefEventsSince` and the two instruments
- [ ] `go test -trimpath ./internal/api -run 'TestBriefEventsSince|Metrics' -count=1`
      → `ok` (with Postgres up, not SKIP)
- [ ] Commit: e.g. `Fetch the Morning Brief event window`

### Task 6 — Tracer: `homePage` assembles and renders the brief

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4, 5]
```

Extend `homePage` (`internal/api/web.go:190`) in the actor-mode branch
(`!in.OpenMode`), after the existing membership/awaiting fetches — reusing
them, never re-fetching:

```go
// Actor mode only: the Morning Brief (032 §9). Open mode has no actor to
// keep a boundary for, so Brief stays nil.
boundary, err := s.st.ActorEventCursor(ctx, sub.ActorID)
// ... webStoreErr on error, as every fetch above.
events, truncated, err := s.briefEventsSince(ctx, boundary)
assigned := map[string][]store.OwnedWork{}
for id := range in.Membership { // bounded by the actor's project count
	work, err := s.st.OpenWorkOwnedBy(ctx, id, sub.ActorID)
	// ... append non-empty
}
repos, err := s.st.ListReposForProjects(ctx, projectIDs)
// keyToProject / repoToProject / Projects map built from the already
// fetched projects slice; Order = the card order homeCards returned.
brief := assembleMorningBrief(morningBriefInputs{ /* ... */ })
```

Record the render metric: `truncated` when the fetch truncated, `empty`
when `brief == nil` or has zero groups, else `rendered`. Pass
`Brief: brief` into the `ui.HomeView`. Open mode: untouched, `Brief` nil,
no cursor read, no metric. **No write of any kind in this handler** —
opening Home does not advance the boundary, and the test below proves it.

Tests in `internal/api/web_test.go`, following `TestHomePageActorTiers`'s
shape (`newOIDCServer`, `webLogin(t, h, "grace")`, seed through the API and
store test helpers):

- `TestHomePageMorningBrief`: seed grace as a member of one project; record
  a tier-2 event (`task.done` with a `task` payload key), a tier-3 event
  (`task.stopped`), and three routine events (`task.created`) attributed to
  the project; GET `/` with the session cookie and assert the section
  heading, the tier-2 text before the tier-3 text, `3 routine updates`
  with no routine item text, and the form with the hidden `cutoff` equal to
  the highest seeded event id.
- `TestHomePageDoesNotAdvanceBoundary`: GET `/` twice; assert the second
  response still shows the same brief content and
  `store.ActorEventCursor` (read via the test store) is still 0.
- `TestHomePageOpenModeHasNoBrief`: `newTestServer` (no session): no
  `Morning Brief` string anywhere in the body.

- [ ] Write the three tests; watch them fail
- [ ] Wire `homePage`; run the render metric
- [ ] `go test -trimpath ./internal/api -count=1` → `ok` (with Postgres up;
      every pre-existing Home test still green)
- [ ] Commit: e.g. `Assemble the Morning Brief on Home`

### Task 7 — The mutation: `POST /home/reviewed`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

One route across every surface it has — which is the web form only; there
is deliberately no `/api/v1` route and no CLI verb for a browser-session
convenience (so `docs/agent-surfaces.md` is untouched).

- `internal/api/router.go`: add `"POST /home/reviewed": guarded(permWebWrite)`
  to `routeGuards`.
- `internal/api/server.go`: register next to the other web POSTs as
  `r.web("POST /home/reviewed", s.navWrap("brief_review", s.requireSession(s.reviewedThroughNow)))`.
- Handler, in `internal/api/morningbrief.go`:

```go
// reviewedThroughNow advances the actor's Morning Brief boundary to the
// cutoff the page displayed (032 §9's explicit "Reviewed through now" —
// the one way the boundary ever moves). Forward-only via the store's
// GREATEST upsert; clamped to the event-log horizon so a forged form value
// cannot mark unseen events reviewed.
func (s *server) reviewedThroughNow(w http.ResponseWriter, r *http.Request)
```

Follow `decideApproval`'s skeleton (`webform.go:385`): `sameOriginForm`
→ 403; `parseWebForm`; parse `cutoff` as int64, refuse `<= 0` or
non-numeric with 422 (`webErr`) and outcome `invalid`; fetch
`EventLogHorizonID` and refuse `cutoff > horizon` with 422, outcome
`invalid`; `AdvanceActorEventCursor(ctx, sub.ActorID, cutoff)` — outcome
`advanced` or `noop` from the returned bool; `http.Redirect(w, r, "/",
http.StatusSeeOther)` so a reload never reviews twice. Plain row write —
no `RecordEvent` (Global Constraints: the subscriber-offset precedent).

Tests in `internal/api/web_test.go` (Postgres, `newOIDCServer` +
`webLogin`):

- `TestReviewedThroughNow`: seed as in task 6; GET `/`, extract the cutoff
  from the form; POST it back with the session cookie → 303 to `/`; GET `/`
  again → routine count gone, tier-2/3 items gone, while a seeded awaiting
  approval and an assigned open task **still render** (persistence across
  the advance); cursor read via the test store equals the cutoff.
- `TestReviewedThroughNowForwardOnly`: POST an older cutoff after a newer
  one → 303, cursor unchanged (noop, not an error — a stale tab is not a
  fault).
- `TestReviewedThroughNowRefusals`: no session → 403 (requireSession);
  `cutoff=abc` and `cutoff=0` → 422; a cutoff beyond the horizon → 422 and
  the cursor untouched.

- [ ] Write the three tests; watch them fail (the route 404s)
- [ ] Add the guard row, registration, and handler; wire `observeBriefReview`
- [ ] `go test -trimpath ./internal/api -count=1` → `ok` — `NewServer`'s
      boot check passes, so the guard row and route agree
- [ ] Commit: e.g. `Advance the Morning Brief boundary on Reviewed through now`

### Task 8 — Journey proof, e2e, and docs alignment

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [7]
```

**The acceptance demonstration** (032 §11: the brief "makes remaining human
judgment obvious without presenting a raw activity firehose") runs where a
session exists. The `e2e/` stack has no login provider, so the full journey
lives in `internal/api` as one public-surface test —
`TestMorningBriefJourney` in `web_test.go` — driving only HTTP: log in via
`webLogin`; create tasks through `POST /api/v1/tasks`; move one to done and
stop one through their public routes; GET `/` and assert the brief shows
the done outcome and the stopped run as items, the creations only as a
collapsed count, and the needs-you state on top; POST `/home/reviewed`; GET
`/` and assert the window content is gone while the needs-you state
remains. Every seeded fact enters through a route, none through the store.

`e2e/smoke_test.go` `assertWebPages` gets the two facts the sessionless
stack can prove: `GET /` contains no `Morning Brief` (open mode has no
boundary), and `POST /home/reviewed` without a session → 403 with no
cursor row created (assert via a second GET, not a store read).

Docs alignment:

- Add **nothing** to `docs/follow-ups.md`: the residue of 032 §9 is already
  declared by the two plans' `covers` blocks, and a second copy drifts.
- Sweep comments: `internal/api/web.go`'s `homePage` doc comment gains the
  brief; `internal/api/home.go`'s package comment still says "no store and
  no request" — keep that true (the derivation stayed pure) and say the
  brief's half lives in `morningbrief.go`. Short and precise, no diary.

- [ ] Write `TestMorningBriefJourney`; green with Postgres up
- [ ] Extend `assertWebPages`;
      `go test -trimpath -race -count=1 -tags e2e ./e2e/ -run TestSmoke` →
      `ok` (local stack per CLAUDE.md)
- [ ] `go test -trimpath -race -count=1 -tags e2e ./e2e/` → `ok`
- [ ] Comment sweep; `go test -trimpath ./...` → green
- [ ] Commit: e.g. `Prove the Morning Brief journey end to end`
