# The project Progress page

The Progress page says how much of each of a project's specs exists and what
moves it next. It is one cockpit page (WL-SPEC-82), and this spec owns its
derivation, layout, write routes and live stream.

## 1. What the page shows {#sec-1}

`GET /projects/{id}/progress` paints each spec as a strip of its sections,
colored by a state derived from the plans that cover the section and the tasks
those plans minted, grouped by the act that moves them next, with the act as a
button. It stores no progress figure; every state is recomputed per request, so
it cannot disagree with `lode doc todo` or the run board. The route answers 404
for a project with no spec. `GET /api/v1/projects/{id}/progress` and `lode doc
progress [--project] [--json]` return the same `model.ProjectProgress`, so an
agent reads what a person sees.

## 2. Derivation {#sec-2}

A plan **covers** a section when one of its covers edges reaches a rule the
section arranges, directly or through that rule's supersession chain
(WL-SPEC-77 §4).

**Plan state** comes from document status and minted tasks (`tasks.plan_doc`).
A `withdrawn` plan is left out of the derivation: it covers nothing. A `stale`
plan counts as `accepted` (plan lifecycle, WL-SPEC-77 §9). Abandoned tasks are
ignored. Task states fall into three classes by rule over
`model.TaskStates`, and a test pins that the classes plus `abandoned` partition
the set: **landed** (states with a delivery rank: `merged`, `deployed_dev`,
`deployed_prod`, `released`), **active** (past `ready`, short of landed:
`in_progress`, `in_review`), **unstarted** (`draft`, `ready`). Open means
active or unstarted.

| Plan state | Condition |
|---|---|
| `draft` | document status `draft` |
| `built` | status `superseded` or `spent`, or `accepted` with at least one task and every task landed |
| `in_progress` | `accepted`, at least one task landed or active, at least one not landed |
| `not_started` | `accepted`, tasks minted, none landed and none active |
| `no_record` | `accepted` and no minted task |

**Section state** takes the state of the furthest-along covering plan, in the
order `built`, `in_progress`, `not_started`, `no_record`, `draft`.

| Section state | Label | Condition |
|---|---|---|
| `built` | Built | best cover is `built` |
| `in_progress` | In progress | best cover is `in_progress` |
| `not_started` | Accepted, not started | best cover is `not_started` |
| `no_record` | Accepted, no execution record | best cover is `no_record` |
| `draft` | Plan awaiting acceptance | every cover is `draft` |
| `unplanned` | Unplanned | no plan covers it |

A section whose rules are all informative is not owed: it counts in neither
the section bar nor the group counts and does not place its spec in a group.
Every other section counts.

**Grouping.** A spec lands in the first group whose condition holds: Active
(any section `in_progress` or `not_started`), Needs planning (any `unplanned`
or `draft`), No execution record (any `no_record`), Built (every section
`built`). Within a group specs sort by most recent document update. Each row
ends with the first applicable **next act**: `N open tasks in <plans>` (every
such plan named in full); `accept <plans>` (every draft plan named);
`N sections unplanned`; `no execution record for <plans>`; `nothing
outstanding`.

## 3. Layout {#sec-3}

The page is reached from the cockpit sidebar's Progress entry. Top to bottom:
the **rally band**, one fixed-height row showing the active rally's id, title,
member count and members landed, or "No active rally" muted; **four counts**,
the group sizes; the **section bar**, every section as a slice in state order
(`built` first, `unplanned` last) with a legend naming state, cell style, count
and one-line meaning, and no percentage; the **groups**, each a heading with
count and meaning then one row per spec; the **rally footer**, a fixed-height
bar at the viewport bottom whose space is always reserved.

A spec row has four columns: reference (the row's only link, to the document
page), title, strip (one cell per section in document order including
subsections, wrapping), next act. Clicking the row anywhere except a link
expands it; expansion state is kept in the browser and survives live updates.
The expanded row shows plans one line each in number order (reference, title,
`requires` list, state pill, a task strip colored landed, active, unstarted
with abandoned omitted, "N landed · M open"; a `no_record` plan shows "no
tasks minted"), a sections table (anchor, heading, state pill, covering plans,
subsections indented), and right-aligned actions.

**Hover text** appears on every cell and reference after a short delay and
never captures the pointer. A section cell: `§3.1 Renewal · In progress ·
WL-PLAN-99`. A task cell: id, title and its **position**, the first line that
applies:

| Position | Fact |
|---|---|
| `merged`, `deployed to dev`, `deployed to prod`, `released` | the task is landed; this outranks any PR or CI fact |
| `queued for merge` | the PR has an open merge-queue entry |
| `checks failed on PR #N` | latest CI run for the PR's head SHA concluded `failure` |
| `checks running on PR #N` | latest CI run is `in_progress` or `queued` |
| `PR #N open` | an open PR carries the task id and neither line above applies |
| `in review` | task `in_review`, no PR fact |
| `claimed by <actor>, <age>` | task `in_progress` with a live lease; age in minutes, hours or days |
| `assigned to <actor>` | task `ready` with an assignee |
| `ready` | task `ready` |
| the task state | anything else |

The PR number is a link only when the tooltip is pinned by a click. The page
shows no percentage: a section is not a unit of work.

## 4. Actions {#sec-4}

Actions sit on the right of an expanded row and on plan lines. A button the
viewer cannot use is rendered disabled with the reason in its hover text, never
hidden. **Every write is a two-step button**: the first click turns it, in
place and at the same size, into a confirmation naming the act in full with
Confirm and Cancel; Cancel, a click elsewhere or Escape restores it. While in
flight it shows busy and ignores clicks. The page never applies the result
itself: the server records the change, the event reaches the page through the
stream, and the row re-renders. A failure restores the button and shows the
server's reason inline. The confirm step protects against mis-clicks only; §5
is the CSRF protection.

| Action | Where | Does |
|---|---|---|
| Accept | each `draft` plan line and a `draft` spec row | exactly what `lode doc accept` does: owner gate, reviewer gate, task minting in one transaction. Enabled only for the document's owner; a refusal is a 409 with the store's reason. |
| Review | every spec row and plan line | opens a WL-SPEC-84 document review through WL-SPEC-84's own review-create route (`POST /api/v1/reviews`). The server decides per request whether that route is registered; while it is not, the button is disabled with hover text saying the review surface is not built. |
| Plan | a spec row with an `unplanned` section | if an open `kind = 'design'` task with `about_doc` the spec exists, the row shows "Planning: WL-nnn" as a link and no button; otherwise mints one with the doc-lifecycle subscriber's title and guard |
| Rally | every spec row | adds the spec's remaining acts to the project's draft rally (below) |
| Confirm Rally, Discard | the rally footer | publishes the draft (`draft` to `ready`, the activation act) or abandons it, dropping its edges |
| Queue for merge, Merge | a pinned task tooltip with an open PR | below |

Rally, Confirm Rally and Discard are disabled without a signed-in session.

**Rally.** Confirming Rally puts into the draft rally one task per remaining
act: every task of the spec's accepted plans that is neither landed nor
abandoned; the open planning task when a section is `unplanned`, minted if
none is open; for each `draft` covering plan a `decision` task "Accept
<plan>?" assigned to the plan's owner, minted when none is open (answering
records the decision and does not accept; Accept does). A `no_record` plan
contributes nothing. Each task is a `blocks` edge into the rally, the only edge
kind a rally carries. Adding is set-like: no duplicates, earlier-minted tasks
reused, a spec with nothing outstanding is a no-op that says so. The draft
rally is created on first add as a `rally` task in `draft` titled "Rally
<date>" owned by the viewer; a project has at most one draft rally and it is
inert until published. With members the footer shows "Rally: N tasks from M
specs". One active rally per project: a second confirm is a 409 and the footer
names the active one. Rally ranking itself is in WL-SPEC-75.

**Queue for merge, or Merge.** The label follows whether the repository's
default branch carries a merge-queue rule (§7). Queue for merge is disabled
when the PR is already queued. Merge is disabled until the latest CI run for
the PR's head concluded `success`. Whether the PR is a draft or mergeable is
not stored; GitHub refuses those and its reason comes back as a 409. Queue for
merge enqueues through the GitHub App's GraphQL `enqueuePullRequest`; Merge
merges through the REST endpoint with the method the repository allows. The
server acts with the App's installation token. Before calling GitHub it
checks that the PR carries the task, is open and is not queued (409
otherwise), and records `pr.merge_requested` with actor, PR and operation;
after the call it records `pr.merge_result` with the outcome and GitHub's
message. GitHub's refusal is a 409, an unreachable GitHub a 502, and an
instance with no GitHub App a 503; with no App both buttons are disabled
with the reason "no GitHub App is configured".
The position line changes on the following stream event; the reply itself
changes nothing on the page.

## 5. Write safety {#sec-5}

Every write is a `POST` under `/projects/{id}/progress/`. One gate
(`beginJSONPost`, `internal/api/webform.go`) enforces rules 1 to 4 and 6 for
all of them, and for the document and task pages' Accept and Publish
buttons. Each route obeys all of:

1. No write on GET; any other method is a 405.
2. Same-origin, strictly: when `Sec-Fetch-Site` is present it must be
   `same-origin`, so `none` is refused, since a script-issued fetch is never a
   user-typed navigation (form routes keep accepting `none` for bookmarks);
   without the header the form routes' origin check applies. A refusal is a
   403.
3. The page script sends `X-Requested-With: lode-cockpit` and the route
   requires it, answering 403 when it is missing; a form cannot set it and a
   cross-origin script triggers a preflight the server never answers.
4. JSON body of at most 64 KiB, JSON reply: 200 with the new fact's id, 403
   when the actor may not act, 404 for a document or task outside the
   project, 409 with a reason when the backbone refuses, 415 for any body
   that is not `application/json`, 422 for a malformed body or an unknown
   field. Never HTML.
5. Guarded in `routeGuards`; the owner gate for acceptance stays in the store,
   so the page cannot grant what `lode doc accept` would refuse.
6. The actor is the session subject; a body naming an actor is a 422.
7. Idempotent: accepting an accepted document is 409, re-adding to a rally is
   a no-op, minting a planning task when one is open returns it, confirming an
   active rally is 409.
8. No `next` or return-URL parameter.
9. The merge route records the session's actor before the outward call and
   never uses a page-supplied token.

Rules 1 to 4 are what stand between a hostile page and a write; a change that
relaxes one must say why the rest suffice. `frame-ancestors 'none'` stays on
every response including JSON replies and fragments. The stream is read-only,
guarded `permWebRead`, scoped to one project, and carries only ids, states and
timestamps; a frame carries no task or document body.

## 6. Live updates {#sec-6}

`GET /projects/{id}/progress/events` is a Server-Sent Events stream shaped like
`GET /api/v1/events/stream` (a poll over the events log with `Last-Event-ID`
resume and a 30-second heartbeat), web-session guarded and filtered to one
project. Without `Last-Event-ID` it starts at the head of the log. The server
resolves each event's task or document id to the project, plan and specs in
one batched read per poll, and emits one frame per event touching the project;
an event touching no row of the project advances the cursor and sends nothing.

| Frame field | Content |
|---|---|
| `event` | the backbone event type |
| `task`, `state` | the task touched and its state after the event |
| `plan`, `specs` | the plan the task or document belongs to and the specs it covers |
| `rally` | the active rally's band when a rally or member was touched |
| `at` | the event timestamp |

On a frame naming a spec the page fetches the fragment `GET
/projects/{id}/progress/spec/{doc}` and swaps the row in place; on any frame it
refreshes the counts, section bar and rally band from `GET
/projects/{id}/progress/summary`. Both fragments read only the specs they
render and the plans covering them. Expansion state, a pinned tooltip and an
in-flight confirmation survive the swap. After a drop the page reconnects with
`Last-Event-ID`, refreshes every row once, and shows "reconnecting" inside the
rally band. A named task cell pulses once (scale and glow, about a second); a
named row highlights its left edge; `prefers-reduced-motion` makes both an
instant color change. Animation changes transform and color only.

Layout stability: band, counts, bar and footer have fixed heights; a row swap
keeps height when section and plan counts are unchanged and otherwise grows
only below the pointer's row; a new spec appears at the end of its group; a
spec changing group moves only when the pointer is off the list; tooltips
never push content.

## 7. Facts the page needs {#sec-7}

- `pull_requests.queued_at`, set when a `merge_group` event with action
  `checks_requested` names the PR (parsed from the group's `head_ref`) and
  cleared when the group is destroyed or the PR closes; ingested through the
  signed-webhook path so `lode inbox import` replays it. The event also
  records the PR's task so the stream can resolve it.
- `repo_branch_rules`: per repository, whether its default branch has a
  `merge_queue` rule, read through the App (`GET
  /repos/{owner}/{repo}/rules/branches/{branch}`). The server refreshes it at
  start, when a repository is mapped, on the `repository_ruleset` webhook and
  once a day. A failed read keeps the last known fact; an unknown repository
  reads as no queue. The page never asks GitHub on a request.
- `tasks.plan_doc` is the only link from a task to its plan. The page never
  infers links from task text. `lode task edit --plan <ref>` sets it on a task
  minted before plans recorded their tasks, logged as a change event on the
  task; it refuses a task already linked to a different plan.

## 8. Assembly and routes {#sec-8}

`internal/store/progress.go` `ProjectProgress(ctx, project, specs)` reads
every fact the derivation needs (specs, sections, plans, covers through
`covered_sections`, minted tasks, open planning tasks, leases, PRs, branch
rules, CI runs, rallies) as one `progress.Input` in a fixed number of
project-wide queries; a non-empty `specs` narrows the read to those specs and
the plans covering them, for the fragments. `internal/progress` is the pure
derivation (`Derive`, `Position`, `MergeAct`, the rally membership and the
frame resolver) and returns `model.ProjectProgress`.
`internal/api/progress.go` and `internal/api/progressmerge.go` hold the
handlers; `internal/ui/progress.templ` renders page and fragments from
`ui.ProgressView`; `internal/ui/assets/progress.js` holds the two-step
buttons, stream client, fragment swap and tooltips under `script-src 'self'`.

| Route | Guard |
|---|---|
| `GET /projects/{id}/progress` | `permWebRead` |
| `GET /projects/{id}/progress/summary` | `permWebRead` |
| `GET /projects/{id}/progress/spec/{doc}` | `permWebRead` |
| `GET /projects/{id}/progress/events` | `permWebRead` |
| `POST /projects/{id}/progress/accept` | `permDocWrite` |
| `POST /projects/{id}/progress/plan` | `permTaskWrite` |
| `POST /projects/{id}/progress/rally/add` | `permTaskWrite` |
| `POST /projects/{id}/progress/rally/confirm` | `permTaskWrite` |
| `POST /projects/{id}/progress/rally/discard` | `permTaskWrite` |
| `POST /projects/{id}/progress/merge` | `permTaskWrite` |
| `GET /api/v1/projects/{id}/progress` | `permProjectRead` |

Metrics: `worklode_progress_writes_total` by route and outcome (`ok`,
`refused`, `conflict`, `error`), `worklode_progress_fragment_renders_total` by
fragment and outcome, the gauge `worklode_progress_streams_active`, and
`worklode_progress_stream_frames_sent_total`.

## 9. Non-goals {#sec-9}

Cross-project roll-ups on the Progress page, and editing documents or task
state from it.

## Designed, not built {#sec-not-built}

| Capability | Section | Builds it |
|---|---|---|
| `withdrawn` plans excluded from the derivation, `stale` plans counted as `accepted` (today both derive as `accepted`) | 2 | WL-967 |
| Sections whose rules are all informative are not owed (today every section counts) | 2 | WL-968 |
| Review posts to WL-SPEC-84's `POST /api/v1/reviews` (the route itself is WL-PLAN-132) | 4 | WL-951 |
