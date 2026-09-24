# Cockpit

The cockpit is Worklode's human-facing web surface: a server-rendered
application shell whose project canvas adapts to the governed state of the
work. It is a projection over the backbone's objects and events and stores
nothing of its own beyond a browser session. This document covers the
navigation shell and session gating, the project Overview with its lifecycle
modes and fact-selected panels, the cross-project inbox, the review surface and
the reading diffs that feed it, the project Progress page with its write routes
and live stream, and the drift and overview queries served to both `lode` and
the read-only web views. The entities it presents keep their owners: tasks in
03-tasks-and-execution.md, documents in 05-documents.md, the knowledge graph in
07-knowledge-graph-and-search.md, permissions in
02-identity-actors-and-secrets.md. Research projects, intake, Crew, milestones,
deliverables and approvals come from WL-SPEC-29 and are only presented here.

## 1. Facts drive the interface

The cockpit stores no parallel completion percentage, editable project health,
manually advanced workflow column, or duplicated delivery or publication
status. Every displayed status keeps its evidence category.

| Category | Meaning |
|---|---|
| declared | intended scope, requirement, owner, deliverable or policy |
| user-reported | an authenticated person's explicit report or decision |
| observed | a fact ingested from GitHub, CI, Payload, a data pipeline, Kubernetes/Flux or another emitter or prober |
| recommended | an AI-produced interpretation; auditable, and never itself satisfying a human gate |

Three layers disclose the same facts progressively. The outcome layer shows
purpose, accountability, next decision, blockers, deliverables and approvals in
plain language. The work layer adds tasks, documents, dependencies, branches,
PRs, datasets and review threads. The evidence layer adds exact revisions,
events, agent sessions, costs, lineage, deploy and runtime facts, and
correlation gaps. The layers are views. They are neither separate products nor
permission boundaries.

## 2. Navigation shell

### 2.1 Top bar

Global navigation is the top bar itself. Left to right it carries the brand (a
link to `/`), five destinations in this order, and the actor controls: the
inbox indicator, the theme toggle, the avatar.

| Destination | Holds |
|---|---|
| Ideas | low-friction capture of a loose idea, ahead of Intake and promotable into it |
| Intake | capture and the Discovery-to-Editorial-Evaluation pipeline |
| Projects | the cross-project portfolio |
| Work | task-oriented saved queries and the ready frontier |
| Knowledge | documents and graph-backed expert views |

`/` (Home), `/reviews` and `/deliveries` keep their routes, pages and
permissions and stay reachable by URL and by any link a page renders. `/`
remains the post-login landing page. Below 880 CSS px the top bar wraps: it
keeps its primary destinations plus a **More** control that expands the rest.
The whole document marks `aria-current="page"` exactly once.

### 2.2 Project sidebar

When a project or candidate dossier is open, the left sidebar gives local
navigation in this order: Overview, Crew, Work, Progress, Deliverables, Reviews,
Decisions, Documents, Activity. Progress appears only when the project has at
least one spec. Global and local navigation stay visually and semantically
distinct. Every project-scoped page renders the sidebar, including the task
detail page at its canonical URL `/projects/<proj>/<kind>/<n>` (`GET /tasks/{id}`
redirects there), whose subject is no sidebar destination, so its
local navigation marks nothing current; the handler adds one `store.GetProject`
lookup for the project's id, name and key. The selected object has one
canonical URL. Browser back and forward and copying that URL work across
full-page and narrow layouts.

**Rule page.** `/projects/<proj>/rule/<n>` shows one design rule: ref and heading, status and version chips, the rendered body, the documents arranging it with a link to each section, the tasks it governs, and its version list, each version at `/<n>/<ver>`. Every `WL-RULE-<n>` in rendered prose links there through `/rules/<ref>`. The page is read-only; the rule editor that saves through `PUT /api/v1/rules/<ref>` is later work.

### 2.3 Inbox indicator

An inbox icon sits on every page immediately left of the theme toggle and links
to `/inbox`. It shows a small red-toned dot when the actor has at least one
inbox item and nothing otherwise. Two constraints follow from the icon being
universal: the has-items answer is computed once per request from the session
subject and carried on the page props every page builds, and it is an existence
check that stops at the first item. The full ranked inbox query runs on
`/inbox` only. There is no numeric count.

## 3. Session gating and rendering

The cockpit is session-gated. OIDC login mints a web session cookie that is
`HttpOnly`, `Secure` and `SameSite=Lax`. Every cockpit route has an entry in
`internal/api/router.go`'s `routeGuards`, and `NewServer` refuses to boot on a
route the table does not name. Reads need `permWebRead`, form writes
`permWebWrite`, and the Progress page's writes `permDocWrite` or
`permTaskWrite`. `internal/api/webform.go`'s `sameOriginForm` refuses a form
POST whose `Sec-Fetch-Site` header says `same-site` or `cross-site`, or, when
the header is absent, whose `Origin` host differs from the request host and the
configured public URL. `Content-Security-Policy` sets `frame-ancestors 'none'`
and `script-src 'self'`. No CSRF token exists.

Pages are HTML assembled on the server from the same `assemble*` functions the
JSON API uses, so presentation logic has one source. `internal/store` readers
feed `internal/api`'s assembly into `model` projection shapes (one model, ADR
036); `internal/api/render.go` maps them onto `internal/ui` view types;
`internal/ui` renders typed, compiled templ components that take those views as
arguments and escape interpolated values by default. `internal/ui` depends on
stdlib, `internal/model` and the templ runtime only. `internal/api` imports
`internal/ui`; the reverse import is forbidden.

Styling is Tailwind CSS v4 through the standalone CLI (CSS-first `@theme`, no
Node.js, no PostCSS). The approved palette and typography are `@theme` tokens.
Variable fonts are self-hosted. Light and dark key on `prefers-color-scheme`
with a `[data-theme]` override. Shell rules that are not utility-shaped (skip
link, visible focus, minimum target size, the narrow-viewport column) are
hand-authored CSS. HTMX is embedded and served from the app's own assets as the
interactivity substrate; no template uses HTMX writes. No Node.js runtime and no
third-party CDN enter the build or the served page. Generated artifacts
(`*_templ.go`, the built stylesheet) are committed and marked generated, so
`go build` and `go test` need none of the tools. CI regenerates and fails on
drift.

## 4. Accessibility and responsive behavior

The target is WCAG 2.2 AA. Primary workflows are keyboard operable, use
semantic landmarks and labelled controls, announce asynchronous results, show
visible focus, avoid colour-only meaning and meet minimum pointer-target size.
Every drag, board or timeline action has a form or menu equivalent. Home,
Intake decisions, project Overview, review and approval work at narrow widths
and on a Chromebook, using full-page detail, progressive disclosure and reduced
columns. Specialist evidence may stay desktop-dense but is never the only path
to a primary decision. Visual style carries no domain meaning; icons, text and
state labels accompany colour.

`./scripts/narrow-check.sh` renders every page component with its fixtures in a
headless browser at 320, 375 and 768 CSS px and 200% zoom. CI installs no
browser, so what runs unattended is the set of markup and stylesheet facts each
finding was fixed by. The settled rules:

- No page scrolls horizontally at 320 CSS px. A data table scrolls inside its
  own labelled, keyboard-focusable container (the one exception WCAG 1.4.10
  grants).
- Nothing is truncated to fit. A row that cannot hold its content stacks.
- Visual order follows document order at every width. The decision rail
  follows the work list on a phone.
- A fixed bar reserves scroll room both at the end of the document and on
  scroll-into-view (2.4.11).
- An identifier (artifact address, branch name, table reference) has no soft
  wrap opportunity, so its box breaks anywhere.
- Muted text `--ink-3` clears 4.5:1 against the page background, the least
  contrasty surface it reaches.
- A control's outline carries its own token at 3:1 (1.4.11). Divider and
  card-edge tokens are decorative and exempt.
- One `:focus-visible` outline, drawn 2px clear of the focused box.
- A rejected form submit is a new document, so the validation message takes
  focus on load and the page title is prefixed `Error:`.
- The light theme's accent is the brand hue darkened toward amber until fill
  and border clear 3:1 on every surface and its ink clears 4.5:1. The dark
  theme keeps the brand value.

1.3.1, 1.3.2, 1.4.3, 1.4.4, 1.4.10, 1.4.11, 1.4.12, 2.4.7, 2.4.11, 2.5.8 and
4.1.2 are verified against the built pages with no outstanding exception.

## 5. The project Overview

### 5.1 Lifecycle modes

The project route lands on the Overview. The shell and local navigation stay
stable; the center canvas and decision rail select one of three modes from
governed facts.

| Mode | Selection rule | Primary job |
|---|---|---|
| Editorial decision | the object is an intake candidate that has not passed both Editorial Evaluation decisions | read the dossier, audit the recommendation, decide |
| Approved launch | promotion created the project and no explicit Enter Research decision exists | confirm accountability, outputs, review requirements, working surfaces, automation authority |
| Operations | the project lead entered Research, or the project is not an intake-promoted investigation | coordinate focus, deliverables, reviews, delivery evidence, people and agents |

Modes are derived from facts; nothing persists or selects them. Promotion switches Editorial
decision to Approved launch atomically. The lead's explicit Enter Research
decision switches Approved launch to Operations, which persists through
Research, Report, Story + Some and External Distribution while its content
adapts. There is no Research setup stage: incomplete launch configuration
blocks only the capability that depends on it, and the lead may enter Research
with noncritical items deferred when the decision surface states the effect.
The Sunstone stage is derived from governed decisions and work (WL-SPEC-29 §1)
and is an orientation; adjacent-stage work overlaps and stays visible. The
dossier, approvals, promotion record and launch configuration live under
Decisions and Documents.

After Research the decision rail presents evidence-backed stage
recommendations. The lead confirms a transition and supplies a reason when
earlier work remains open; that work appears as carryover under its original
milestone. Re-entering an earlier stage appends a transition with a reason and
keeps the Operations cockpit. When required deliverables are terminal the rail
may recommend closure; the lead reviews unfinished work and explicitly closes
the project and its active Crew.

### 5.2 Stage due dates

The stepper's five labels are fixed organization-wide: Launch, Research, Report,
Story + Some, Distribution. Launch folds Discovery, Selection and Editorial
Evaluation. A project cannot reshape this vocabulary. Each stage may carry an
optional **due date**, rendered under the stage title as a smaller line
("Unset" or a date) and settable by clicking it. The clickable text is a
focusable link opening a plain form that posts to the project, guarded
`permWebWrite`, lead only. A due date is a target: setting it is no decision,
stage derivation never reads it, and a new date replaces the old with no
history. It is keyed on the five-label set, so Launch can carry one.

### 5.3 Focus and the next decision

Every Overview presents at most one primary **next decision**, naming the
decision in ordinary language, the accountable actor or role, the exact
governed revision being decided, why it is ready or blocked, what each
permitted action changes, and the material and contrary evidence available.
Other concerns stay in a secondary list without equal weight. The lead may pin
any governed project object as the current focus with a short note; pinning
changes orientation and ranking display only. The cockpit never displays a
project-completion percentage. Progress is the state of the decisions,
deliverables, reviews, dependencies and observed facts that define the project.

### 5.4 Definition of done

The Overview derives a definition of done from deliverable objects and their
required evidence and approvals; no separate checklist exists. A custom
deliverable editor asks for name, description and an optional URL.

## 6. Panels selected by fact

The Operations cockpit carries a fixed set of four panels. A panel appears when
the project holds the facts it renders, and an empty panel is omitted rather
than rendered blank: a software project whose CI panel disappears has lost its
webhook, and an empty box would hide that. There is no project type, no `kind`
column and no per-project panel configuration; a research project that
acquires a repository starts showing delivery panels on its own. Nothing is
stored: every panel is assembled per request from `docs`, `tasks`,
`pull_requests` and `ci_runs`. Every value is *observed* except the documents
panel's coverage, which is *declared*. A fifth panel is a change to this
section.

| Panel | Appears when | Content |
|---|---|---|
| Documents | the project owns at least one spec | one row per spec: reference, title, sections covered by a plan against sections total, planning outcome (full, partial, none, per 06-design-queries-intents-decks.md), open tasks referencing it. Coverage is per document and never summed or rolled up. |
| Work mix | the project has an open task | open tasks per `wlc:TaskKind`; open is the task board's own store predicate; kinds with no open task omitted |
| Pull requests | `pull_requests` holds an open row for a mapped repository | repository, number, title, link, age; oldest first |
| Continuous integration | `ci_runs` holds a run for a mapped repository | running now: every run not `completed`, with workflow and elapsed time; then the last 20 completed runs across all the project's repositories, newest first, with repository, workflow, conclusion, start, duration |

Assembly: `internal/store` readers feed `assembleProjectCockpit`, which extends
`model.CockpitProjection` with the four panel shapes; `render.go` maps them onto
`ui.CockpitView`. Planning outcome is computed by `designdoc.Todo`, shared with
`lode doc todo` through an exported `[]model.Doc` to `[]designdoc.CorpusDoc`
conversion in `internal/designdoc`, so the page and the CLI cannot disagree.

## 7. Intake and promotion

Intake capture requires only a title and description. The intake portfolio may
show deduplicated AI-originated candidates, but a named person must adopt one
before Selection begins. The candidate view uses the cockpit shell in Editorial
decision mode while the governed object stays the intake task.

Selection presents a versioned dossier: current question, strongest evidence,
evidence against, unknowns, hypothesis changes, source and claim links, and the
AI recommendation, with a layered audit path (recommendation, claims and
evidence, immutable run record with effective policy). The interface records
the two Selection decisions separately: authorize bounded pre-research after
Gate 1, then accept, narrow, park or stop after Gate 2. Editorial Evaluation
records separate Editor and Science Lead decisions on the exact dossier
revision. There is no generic "Create project" action: when both approve, the
WL-SPEC-29 §8.1 promotion transaction runs and redirects to the new project's
Approved launch cockpit. Rejection by either role prevents promotion but leaves
the candidate open, with the rejecting role accountable for revision,
reconsideration, parking or closure. Overriding an AI recommendation requires a
visible rationale and the joint approval WL-SPEC-29 defines.

## 8. Crew and agents

People see **Crew**, never `participants`. A person may show several role
labels; exactly one project lead is visually distinct. An agent is shown as a delegate
on work and is excluded from Crew and ownership. An automatic action's actor label is
"Worklode, on behalf of _User_", linked to the authorization and event. An
approver or reviewer appears in Crew only when participant facts say they
actively contribute. An invited external expert may appear before identity
linkage with ownership and approval actions disabled until the invitation is
linked to a Keycloak actor; linking updates the same displayed person and keeps
the history. Removing a Crew member opens a responsibility review listing their
open tasks, decisions and reviews, and completes only after each is reassigned
or explicitly unassigned. Past roles stay visible.

## 9. Deliverables and review lanes

Dataset or data product, reproducible analysis, methodology, scientific report
and story are distinct governed targets with individual readiness. Three
decisions never collapse: code and analysis evidence at an exact commit,
methodology review at an exact document revision, report review at an exact
document revision.

The analysis review shows the exact repository and commit, environment lock,
entry point, tests and CI, dataset snapshots and lineage, generated outputs,
and the diff from the previously reviewed revision. Its required reviewer is a
qualified data-science or engineering peer who is not the author. When a
deliverable designates a newer commit the previous approval stays on its
revision and the newer candidate is visibly unreviewed. Methodology and report
use an approval-oriented view with the submitted revision, supporting
deliverables, comments, assigned qualified reviewers and independent Approve or
Request changes actions. The methodology view binds exact analysis and dataset
revisions; the report view binds exact methodology, analysis and evidence
revisions; both expose a review graph. For a possible downstream impact the
owner submits an impact note and a qualified prior approver confirms or reopens.
A self-review exception needs policy permission and a different authorized
actor's approval before review; both facts appear beside the decision.

One review session may present several targets (a spec and its plans) but
records a separate decision for each. Specs are always reviewed; plans and
task-result review are optional, and tasks acquire no review requirement by
existing. A material change reopens the changed target and exposes an impact
decision for dependents; unrelated approvals stay intact. Approval displays
name exact version, actor, role, time, source and any policy exception. For
work no Worklode task tracks, GitHub PR review stays the surface and the
cockpit shows normalized review and check evidence with a source link; for a
task's change, the review of record is §13.

## 10. Automation and unattended execution

Automation appears as scoped authority on the governed object; it is never listed as an assignee. Its
control shows the effective rule in verbs and previews what the next event will
do. Instance configuration supplies named presets, by default Manual, Planning
assist, Execute accepted plans and Bounded autopilot; users choose among them
and may change later. Every automatic action records the authorizing actor and
policy version. Invariants:

- no planning agent starts before the governing spec is accepted;
- spec acceptance is an explicit human decision;
- plan review and acceptance is a separate decision when a plan is used;
- accepting a plan mints exactly its declared tasks;
- automatic execution acts only on eligible ready tasks within saved bounds.

The policy preview exposes three hand-offs separately. On spec acceptance the
planning-decision task is minted; policy decides whether it waits for a person
or is delegated to a planning agent, and "no plan" stays an explicit human
outcome. A planning agent drafts plans and never accepts them. On plan
acceptance the declared execution tasks are minted; policy decides whether they
stay ready, receive suggested delegates, or dispatch automatically. Combining
targets in one review surface never combines these decisions or their events.

Before an unattended run starts, one confirmation surface shows projects and
repositories, eligible kinds, agent pools, concurrency, token and spend budget,
expiry, retry and stop conditions, environment authority, 1Password readiness
by symbolic requirement name only (never values or `op://` references),
accountable user, and remaining human gates. The live run groups work into
Ready, Running, Waiting, Needs judgment, Failed and Completed; each active item
shows owner, delegate, lease age, last durable event, cost, PR and check state,
and next expected signal. Deterministic tool or infrastructure failures may
retry within policy; ambiguous failures pause the affected dependency branch
while independent branches continue. Pause stops new dispatch without
rewriting task state.

## 11. Home and the Morning Brief

Home (`/`) is the post-login destination. It combines assigned human work,
approvals, supervised agent work, and a Morning Brief computed from governed
events since the actor's previous boundary. The brief groups by project or
pinned focus and orders: decisions and exceptions needing the actor; material
outcomes and changes; runs that stopped or reached a bound; routine successful
work, collapsed. Unresolved items persist across briefs. Opening Home never
marks the brief consumed; an explicit **Reviewed through now** action advances
the actor's event boundary to the displayed cutoff. Worklode schedules no
wake-up, email, chat or off-hours notification for orchestrated work; production
alerts stay in the operational alerting system.

## 12. The inbox

`GET /inbox` lists what is waiting on the signed-in actor across every project
they participate in, ranked so the item that unblocks the most other work comes
first. It adds ordering only: every item is reachable
without it, and it filters nothing an actor may read. It is guarded by the same web-read permission as the other cockpit
pages and adds no permission, role or stored state. It reports through
`worklode_*` metrics. It is unrelated to `/api/v1/inbox*`, the GitHub triage
import.

**Items.** Reviews are open `approvals` rows (`awaiting`, `changes_requested`)
with `entity_kind = 'pr'`: *assigned to* the actor when `required_actor` is the
actor; *owned by* the actor when they authored the PR (GitHub login matched
through `actors.expected_github_login`, read non-transactionally) and someone
else decides. Work is tasks in an active state: *assigned* when the actor is
assignee, *owned* when they created it and someone else or nobody is assigned.

**Order.** Every member sees, in this order:

1. reviews assigned to them;
2. open reviews with no `required_actor` in projects they lead (`is_lead`);
3. reviews they own;
4. work assigned to them;
5. work they own;
6. other in-progress work in their member projects.

The order descends from "blocked on this exact person" to "happening near this
person"; it is the one-primary-decision rule of §5.3 applied across a
portfolio. Review buckets order oldest-open first by approval creation time,
ties by id. Work buckets order by the deterministic exception ranking det-v1
(`docs/research/cockpit-exception-ranking.md` §6): one entry per root cause,
scored by best held priority, fan-out and age. The ranker is fed the
all-projects read so blocked-by chains resolve across project boundaries, and
concerns are filtered to membership afterwards; filtering first would truncate
chains and name the wrong root. A decisions bucket (decision-kind tasks,
05-documents.md) joins the order.

## 13. The review surface

### 13.1 The review is the API

The durable object is a **review**: a subject, threads anchored into it, an
ordered series of rounds, and a verdict per reviewer. All of it lives in
Postgres, is written through `/api/v1`, is emitted to the event log, and is
readable by anything holding a token. A terminal, an agent with no browser, and
a browser desk are three equal clients. Every review verb works with no crit
and no browser; the moment a verdict can only be cast from a browser, the
agent-reviewer path is second-class.

One review object covers documents and changes, with two anchor kinds. The
subject of a change review is the **task** (a task may carry several PRs and
`in_review` is a task state), which makes a cross-repo change reviewable.
Threads, intents, resolution, rounds and verdict invalidation are identical
across the two subjects.

Worklode owns natively: persistence and the anchor model that survives a
force-push; identity, so a verdict names an authenticated actor; the reviewer
set and gate; provenance through the event log; cross-machine question
delivery; blocking a task on a review. crit (Go, MIT) is run unmodified as an
interim terminal client that owns the round UI; nothing server-side knows it
exists. crit's `Comment` shape is adopted as contract for anchoring (anchor text
as identity, line numbers as hints, four-stage recovery: LCS remap, fuzzy match
with a minimum-length guard, whole-file scan nearest the predicted line, then
`drifted`), and a golden copy of its `Comment` and top-level JSON shapes is
checked in so upstream drift is a red test.

### 13.2 Anchors

A thread anchors to content because positions do not survive the author
changing the artifact. Document threads anchor to a frozen `{#sec-N}` anchor
plus the quoted text. Change
threads anchor to `(repo, path, side, line_number)` plus `anchor_text` (the
exact anchored line at creation) and the new-side blob sha; after a force-push
the text re-anchors through the recovery ladder. A thread whose anchor no
longer resolves becomes `obsolete`; it is never deleted or re-pointed.

### 13.3 Threads and intents

| Intent | Means | Obliges |
|---|---|---|
| `note` | a remark | nobody |
| `change_request` | change this | the author, before the next round |
| `question` | explain this | the author, now, without touching the artifact |

A question is read-only and server-enforced: while a review has an open
`question` thread the author may reply, and a round submitted by the author is
refused. If the question really asks for a change, the author edits and opens
a new round, a different act. A question is answered synchronously in-thread by
the agent that authored the change and still holds the lease; nothing is
minted, the lease is kept, the task stays `in_review`. If the author cannot
answer because the plan or spec does not cover the case, `lode task escalate`
applies and the thread carries the escalation's task id. A question asked of an
author whose lease expired stays open and folds into the next round.

A bare note is a thread with `review_id IS NULL`: `lode doc note` writes into
`review_threads` with the same anchor, author and resolution verb and nothing
obliged to clear it, so "what is outstanding on §4.3, from any source" is one
query.

### 13.4 Guides and the honesty rule

A guide is the mirror image of `lode task brief`: an overview, per-file
orientation, an explicit flag on what deserves scrutiny, and skim spans on what
does not, shaping a reviewer's attention. `GET /api/v1/reviews/{id}/guide`
serves it as JSON; `lode review show --json` embeds it. crit has no guide
concept, so the guide is materialized as a file crit is pointed at. The guide
is optional everywhere.

Composition with reading diffs (§14): the reading diff's summary becomes the
guide's `overview`, labelled machine-produced; its omissions become skim spans
**only** on a review marked `focused`, and an unfocused guide skims nothing;
meat's file ordering becomes the guide's file order. Where no reading diff
exists the guide carries no overview and no skims and is authored by the agent
under review.

**No agent may lower the gate on its own work.** A skim is advisory to a human
and authoritative to a reviewer agent, so three rules are enforced server-side:
the guide handed to a reviewer agent is produced by the backbone from the
author's proposal plus policy, and `GET .../guide` is its only source; an author's proposed skim is refused on a file it edited
non-mechanically (the mechanical set: lockfiles, generated output, vendored
code, snapshots, import-only and formatting-only blocks), with the rule that
fired; self-approval is refused, so a verdict from the actor holding the task's
lease or the agent session that authored the round is rejected at the API.
`focused` is stored on the round so "was this approval cast over a narrowed
diff, and who narrowed it" is a query.

### 13.5 Schema

Nothing duplicates `doc_reviewers` or `approvals`: a document review reads its
reviewer set from `doc_reviewers` and writes its verdicts into `approvals`. Rows
are never deleted.

| Table | Key columns and constraints |
|---|---|
| `reviews` | `id`; exactly one of `task_id`, `doc_id` (`reviews_one_subject`); `subject` in `change`, `doc`, agreeing with the set id; `state` in `open`, `changes_requested`, `approved`, `closed`; `base_ref` (git sha for a change, `docs.version` for a document); `round`, `next_seq`, `opened_by`, `opened_at`, `closed_at`. Unique partial indexes give one open review per task and per doc. |
| `task_reviewers` | `(task_id, actor_id)`, `assigned_at`; the change-side counterpart of `doc_reviewers`, cascading on task delete |
| `review_rounds` | `(review_id, round)`, `actor_id`, `client` in `crit`, `web`, `cli`, `agent`; `base_ref`; `focused boolean`; `overall_note`; `verdict` in `approved`, `changes_requested`; `submitted_at`. One row per Send. |
| `review_guides` | `(review_id, round)`, `guide jsonb`, `proposed_by`, `skims_refused`, `from_reading_diff`, `created_at`; keyed by round so a stale guide is identifiable |
| `review_threads` | `id`; nullable `review_id` (NULL for a bare note); denormalized subject anchors `doc_id`, `doc_anchor`, `task_id`, `repo`, `path`, `side` in `additions`, `deletions`, `line_number`, `anchor_text`, `blob_sha`; `raised_by_task`, `raised_by_session`; `intent`; `status` in `open`, `resolved`, `obsolete`; `author_id`, `created_at`, `resolved_by`, `resolved_at`. Indexes on open threads per review and on `(doc_id, doc_anchor)`. |
| `review_messages` | `id`, `thread_id`, `review_id`, `seq` (unique per review), `actor_id`, `role` in `reviewer`, `author`, `body`, `created_at` |

### 13.6 API and `lode` verbs

| Route | Does |
|---|---|
| `POST /api/v1/reviews` | open; reviewers come from `doc_reviewers` or `task_reviewers` |
| `GET /api/v1/reviews?subject=&reviewer=&state=` | list |
| `GET /api/v1/reviews/{id}` | review, threads, current guide; the first GET by an assigned reviewer sets a document to `in_review` |
| `GET /api/v1/reviews/{id}/guide` | guide JSON |
| `POST /api/v1/reviews/{id}/rounds` | submit a round |
| `GET /api/v1/reviews/{id}/rounds/{n}` | one round |
| `POST /api/v1/reviews/{id}/threads` | open a thread |
| `POST /api/v1/reviews/{id}/threads/{tid}/messages` | reply |
| `POST /api/v1/reviews/{id}/threads/{tid}/resolve` | resolve |
| `POST /api/v1/reviews/{id}/verdict` | cast a verdict; written to the current `review_rounds` row |
| `GET /api/v1/reviews/{id}/events?after=<seq>&timeout=` | long-poll; 204 on timeout |
| `POST /api/v1/tasks/{id}/reviewers` | wholesale replace of `task_reviewers` |

| Command | Does |
|---|---|
| `lode review add <task\|doc>` | open |
| `lode review list [--mine] [--open]` | list |
| `lode review show <id> [--json]` | threads, guide, reading diff |
| `lode review guide <id>` | the current guide |
| `lode review set guide <id> [--focused]` | regenerate the guide |
| `lode review exec <id>` | launch crit with hooks |
| `lode review note <id> --anchor <a> --intent question\|change_request\|note --body` | open a thread |
| `lode review note <id> --thread <tid> --body` | reply |
| `lode review resolve <thread>` | resolve |
| `lode review set verdict <id> approved\|changes_requested` | verdict |
| `lode review listen <id> [--timeout <s>]` | author side; blocks, prints one tagged JSON envelope, exits; `--timeout` prints nothing and exits 0; non-zero means no open review |
| `lode review import < result.json` | crit's `on_finish_*` hook target |
| `lode task set reviewers <id> a,b,c` | mirrors `lode doc set reviewers` |

**The crit bridge** is client-side only, in `internal/cmd`: resolve the review,
fetch the guide, materialize it to a temp file outside the working tree; launch
crit against the subject with `on_finish_*` hooks pointing at `lode review
import`; on finish crit pipes its JSON to stdin, and ingest POSTs a round with
`client=crit` and opens a `question` thread per open question; `lode review
listen` long-polls `/events` on the author side. crit anchors documents by line,
so `lode doc show <ref> --anchors <map>` writes the document plus a sidecar mapping
line ranges to `{#sec-N}`, and ingestion maps a comment at line 412 to a thread
on `#sec-4.3` with the quote. The export is read-only and regenerable; nothing
is read back from it.

**Clients.** Human at a terminal desk: crit plus `lode review exec` (v1's
primary human surface). Human at a terminal without a desk: `note`,
`set verdict`. Reviewer agent: `show --json`, `note`, verdict, with guide and
reading diff as brief. Author agent: `listen`, `note --thread`, `rounds/{n}`. Web UI:
read-only review view in v1, producing no rounds, behind the OIDC gate.

### 13.7 Verdicts and gates

The gates stay where they are; the review supplies evidence.

- Documents: not accepted until every `doc_reviewers` reviewer approves;
  `lode doc accept` reads `approvals` (05-documents.md).
- Changes: opening a review over a task's change moves it to `in_review`; a
  round carrying any open `change_request` thread, or a `changes_requested`
  verdict, returns it to `in_progress` and releases nothing, since the author
  holds the lease. The merge transition stays the task state machine's.
- An unresolved `change_request` thread on the current round refuses an
  `approved` verdict; an open `note` or `question` does not. Thread resolution
  and verdict are orthogonal signals sharing a UI.
- A new round from the author clears every approval cast against an earlier
  round.
- `lode review add` mints a `kind = 'review'` task per reviewer in
  `doc_reviewers` or `task_reviewers`, assigned via `tasks.assignee`, so an
  agent reviewer picks it up through `lode task claim --next --kind review`. This routes
  the work and grants no authority.

### 13.8 Degradation, metrics, events

No crit and no browser: every verb works and `lode review show` prints the
guide as text and the diff raw. No reading diff: the guide ships without
`overview` or skims. No guide: crit and `show` work. Stale guide
(`review_guides.round` behind `reviews.round`): `lode review set guide` regenerates.

Metrics live in a nil-safe `Metrics` in `internal/review/metrics.go` with every
label bounded by a `CHECK` or enum.

| Metric | Type | Labels or buckets |
|---|---|---|
| `worklode_review_rounds_total` | counter | `subject`, `client` |
| `worklode_review_threads_total` | counter | `intent` |
| `worklode_review_open_threads` | gauge | `subject` |
| `worklode_review_question_latency_seconds` | histogram | 5, 15, 60, 300, 900, 3600 |
| `worklode_review_time_to_verdict_seconds` | histogram | 300, 1800, 7200, 86400, 604800 |
| `worklode_review_verdicts_total` | counter | `verdict`, `reviewer_kind` in `human`, `agent` |
| `worklode_review_skimmed_blocks_total` | counter | `decided_by` in `author`, `policy` |
| `worklode_review_guide_skims_refused_total` | counter | `rule` |

Events, emitted in the transaction that makes the change: `wl:ReviewOpened`,
`wl:ReviewRoundSubmitted` (counts, `focused`, `client`),
`wl:ReviewQuestionAsked`, `wl:ReviewQuestionAnswered` (first author reply),
`wl:ReviewThreadResolved` (`resolved` or `obsolete`), `wl:ReviewVerdictRecorded`,
`wl:ReviewClosed`. `ns/ontology.ttl` carries `wl:Review`, `wl:ReviewThread`,
`wl:reviews`, `wl:reviewer`, `wl:threadIntent`, `wl:reviewVerdict` and one
`wl:Event` subclass per event; `ns/concept.ttl` carries `wlc:ReviewIntent` and
`wlc:ReviewVerdict`. Go constants and `CHECK` fragments are generated from
`ns/`.

Out of scope: a native web review desk before two independent clients prove
the API (`rounds_total{client}` decides when), applying a review automatically,
mirroring GitHub PR review threads, review analytics, and threads outside an
anchor (a review-level remark is the round's `overall_note`).

## 14. Reading diffs

A **reading diff** is a unified diff reduced by meat (Apache 2.0, Go) to the
part a senior reviewer needs. meat asks a model for an edit plan (removals,
folds, single-line elisions) and applies it to the immutable input, so the
model never authors displayed text and the result is provably a subset of the
real diff. Three normative rules:

- A reading diff is a rendering. The raw diff is one action away on every
  surface, and nothing in the backbone derives from the reading diff.
- No automated gate reads it. It never advances a task, satisfies a review or
  feeds a policy check.
- Its provenance travels with it. Every surface labels it
  `reading diff · <model> · N of M lines shown` and offers the raw diff.

meat's edit-plan types are unexported, so skim spans are recovered by
line-diffing `SmartDiff` against the raw diff: every hunk missing from
`SmartDiff` was elided. The recovery is exact because meat cannot rewrite
lines. `internal/reading.Diff` carries `SmartDiff`, `Summary`, `SkimSpans
[]LineRange{Path, StartLine, EndLine}`, `RubricHash`, `Model`, `InputTokens`,
`OutputTokens`, computed once and cached. `Abridger.Abridge(ctx, unifiedDiff,
repoRoot)` has one meat-backed implementation and a fake for tests; no test
makes a model call. meat is a dependency pinned at a pseudo-version in `go.mod`.
The `meat.Model` is wrapped by a decorator that observes every turn; v1 uses
meat's built-in Anthropic and OpenAI clients.

| Variable | Meaning |
|---|---|
| `LODE_READING_MODEL` | model id; Anthropic ids route to Messages, everything else to OpenAI Responses. Unset disables the feature. |
| `LODE_READING_API_KEY` | credential |
| `LODE_READING_BASE_URL` | optional gateway origin |
| `LODE_READING_MAX_DIFF_BYTES` | refusal ceiling, default 1 MB, recorded as terminal `too_large` |
| `LODE_READING_CONCURRENCY` | worker bound, default 2 |

**Production** is server-side, once for the org. The server's GitHub App
fetches the diff (`GET /repos/{owner}/{repo}/pulls/{n}` with the diff media
type) and the tree (`AppAuth.Tarball` at the head sha, extracted to a bounded
temp directory passed as meat's `RepoRoot`, deleted when the run ends; a
missing tarball degrades to diff-text-only). A subscriber named `reading-diff`
on the event log fires on a `pull_request` ingest event for `opened` or
`synchronize` and on a task entering `in_review`. It resolves the diff
identity, inserts a `pending` row idempotently, and acks; it never abridges. A
worker in the server drains pending rows with `FOR UPDATE SKIP LOCKED`,
unordered. The cache is the queue: a second PR whose diff hashes identically
joins the pending row. Failure retries with backoff to `attempts = 3`, then is
terminal; `failed` and `too_large` never retry and never block anything.

| Table | Columns |
|---|---|
| `reading_diffs` | PK `(diff_sha256, rubric_hash, model, speed)`; `state` in `pending`, `ready`, `failed`, `too_large`; `smart_diff` (NULL while pending, may be empty when ready, meaning the change is entirely mechanical); `summary`; `skim_spans jsonb`; `input_tokens`, `output_tokens`, `chunks`, `attempts`, `last_error`, `produced_day date`, `requested_at`, `produced_at`; `ready` requires `smart_diff` and `produced_day`; partial index on pending by `requested_at` |
| `reading_diff_sources` | PK `(repo, pr_number, head_sha)`; `base_sha`, `task_id`, FK to the four-part reading diff key, `requested_at` |

Rows are never garbage-collected in v1. Tokens are stored and cost is derived
through `store.ModelPriceFor(model, speed, produced_day)`, attributed once to
the producing task via `reading_diff_sources.task_id` and shown on `lode task
cost` as a line distinct from session cost (08-agent-harness-and-sessions.md).

| Surface | Behaviour |
|---|---|
| `GET /api/v1/pull-requests/{repo}/{number}/reading-diff` | state, summary, smart diff, skim spans, model, tokens, derived cost; `pending` returns 202 with no body; 404 when the feature is off |
| `lode task diff <id> [--pr <repo>#<n>]` | renders with the summary above; `--raw` prints the unabridged diff; `--wait` blocks on pending |
| `lode task show` | prints the summary line when the task's PR has a `ready` row |
| web task page | reading diff inline with a control to reveal the raw diff |
| `lode task brief` for `kind = 'review'` | the reading diff is part of the reviewer agent's brief |

With `LODE_READING_MODEL` unset the subscriber is not registered, the worker
does not start, the endpoint returns 404, and every other surface renders as
before; no deployment may depend on an outbound model call to show a task.

Metrics (`internal/reading/metrics.go`): `worklode_reading_diff_requests_total{outcome}`,
`worklode_reading_diff_duration_seconds` (5 to 600 s buckets),
`worklode_reading_diff_cache_lookups_total{result in hit, miss, joined_pending}`,
`worklode_reading_diff_model_turns_total{result}`,
`worklode_reading_diff_tokens_total{class}`, `worklode_reading_diff_pending`
gauge, `worklode_reading_diff_chunks` histogram (1 to 32). One event,
`wl:ReadingDiffProduced`, is emitted when a row reaches a terminal state, with
`wl:subject` naming the PR. A reading diff gets no `wl:` class and is not
projected to the knowledge graph. Out of scope: abridging design documents,
retention, per-class token accounting.

## 15. The project Progress page

`GET /projects/{id}/progress` paints each spec as a strip of its sections,
colored by a state derived from the plans that cover the section and the tasks
those plans minted, grouped by the act that moves them next, with the act as a
button. It stores no progress figure; every state is recomputed per request, so
it cannot disagree with `lode doc todo` or the run board. The route answers 404
for a project with no spec. `lode doc progress [--project] [--json]` prints the
same `model.ProjectProgress`.

### 15.1 Derivation

**Plan state** comes from document status and minted tasks (`tasks.plan_doc`).
Abandoned tasks are ignored. Task states fall into three classes by rule over
`model.TaskStates`, and a test pins that the classes plus `abandoned` partition
the set: **landed** (states with a delivery rank: `merged`, `deployed_dev`,
`deployed_prod`, `released`), **active** (past `ready`, short of landed:
`in_progress`, `in_review`), **unstarted** (`draft`, `ready`). Open means
active or unstarted.

| Plan state | Condition |
|---|---|
| `draft` | document status `draft` |
| `built` | status `superseded`, or `accepted` with at least one task and every task landed |
| `in_progress` | `accepted`, at least one task landed or active, at least one not landed |
| `not_started` | `accepted`, tasks minted, none landed and none active |
| `no_record` | `accepted` and no minted task |

**Section state** takes the state of the furthest-along covering plan, in the
order `built`, `in_progress`, `not_started`, `no_record`, `draft`, after
setting aside covers at `coverage: none`.

| Section state | Label | Condition |
|---|---|---|
| `built` | Built | best cover is `built` |
| `in_progress` | In progress | best cover is `in_progress` |
| `not_started` | Accepted, not started | best cover is `not_started` |
| `no_record` | Accepted, no execution record | best cover is `no_record` |
| `draft` | Plan awaiting acceptance | every cover is `draft` |
| `unplanned` | Unplanned | no plan covers it |
| `bound` | (not drawn) | every cover is at `coverage: none` |

A section whose only non-`none` covers are `partial` also carries the
**partial** flag, discharged by `lode task decompose` or a completing plan. Every
state except `bound` is owed; counts count owed sections. A **bound only**
section has no cell, no bar slice, no legend entry and is in no count; the
expanded sections table lists it muted.

**Grouping.** A spec lands in the first group whose condition holds: Active
(any section `in_progress` or `not_started`), Needs planning (any `unplanned`
or `draft`), No execution record (any `no_record`), Built (everything owed is
`built`). Within a group specs sort by most recent document update. Each row
ends with the first applicable **next act**: `N open tasks in <plans>` (all
every such plan named in full); `accept <plans>` (every draft plan named);
`N sections unplanned`; `no execution record for <plans>`; `nothing
outstanding`.

### 15.2 Layout

Top to bottom: the **rally band**, one fixed-height row showing the active
rally's id, title, member count and members landed, or "No active rally"
muted; **four counts**, the group sizes; the **section bar**, every owed
section as a slice in state order with a legend naming state, cell style,
count and one-line meaning, and no percentage; the **groups**, each a heading
with count and meaning then one row per spec; the **rally footer**, a
fixed-height bar at the viewport bottom whose space is always reserved.

A spec row has four columns: reference (the row's only link, to the document
page), title, strip (one cell per section in document order including
subsections, wrapping; partial sections show an inset ring), next act.
Clicking the row anywhere except a link expands it; expansion state is kept in
the browser and survives live updates. The expanded row shows plans one line
each in number order (reference, title, `requires` list, state pill, a task
strip colored landed, active, unstarted with abandoned omitted, "N landed · M
open"; a `no_record` plan shows "no tasks minted"), a sections table (anchor,
heading, state pill, covering plans, subsections indented), and right-aligned
actions.

**Hover text** appears on every cell and reference after a short delay and
never captures the pointer. A section cell: `§3.1 Renewal · In progress ·
WL-PLAN-99`. A task cell: id, title and its **position**, the first line that
applies:

| Position | Fact |
|---|---|
| `queued for merge` | the PR has an open merge-queue entry |
| `checks failed on PR #N` | latest CI run for the PR's head SHA concluded `failure` |
| `checks running on PR #N` | latest CI run is `in_progress` or `queued` |
| `PR #N open` | an open PR carries the task id and no CI run is recorded |
| `in review` | task `in_review`, no PR fact |
| `claimed by <actor>, <age>` | task `in_progress` with a live lease |
| `assigned to <actor>` | task `ready` with an assignee |
| `ready` | task `ready` |
| `merged`, `deployed to dev`, `deployed to prod`, `released` | the delivery state |

The PR number is a link only when the tooltip is pinned by a click. The page
shows no percentage: a section is not a unit of work.

### 15.3 Actions

Actions sit on the right of an expanded row and on plan lines. A button the
viewer cannot use is rendered disabled with the reason in its hover text, never
hidden. **Every write is a two-step button**: the first click turns it, in
place and at the same size, into a confirmation naming the act in full with
Confirm and Cancel; Cancel, a click elsewhere or Escape restores it. While in
flight it shows busy and ignores clicks. The page never applies the result
itself: the server records the change, the event reaches the page through the
stream, and the row re-renders. A failure restores the button and shows the
server's reason inline. The confirm step protects against mis-clicks only; §15.4
is the CSRF protection.

| Action | Where | Does |
|---|---|---|
| Accept | each `draft` plan line and a `draft` spec row | exactly what `lode doc accept` does: owner gate, reviewer gate, task minting in one transaction. Enabled only for the document's owner; a refusal is a 409 with the store's reason. |
| Review | every spec row and plan line | opens a §13 document review. Disabled, with the reason in hover text, when the server has no review routes registered, decided per request. |
| Plan | a Needs planning row with an `unplanned` section | if an open `kind = 'design'` task with `about_doc` the spec exists, the row shows "Planning: WL-nnn" and no button; otherwise mints one with the doc-lifecycle subscriber's title and guard |
| Rally | every spec row | adds the spec's remaining acts to the project's draft rally (below) |
| Confirm Rally, Discard | the rally footer | publishes the draft (`draft` to `ready`, the activation act) or abandons it, dropping its edges |
| Queue for merge, Merge | a pinned task tooltip with an open PR | below |

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
names the active one. Rally ranking itself is in 03-tasks-and-execution.md.

**Queue for merge, or Merge.** The label follows whether the PR's base branch
carries a merge-queue rule. Queue for merge is enabled whenever the PR is open
and not a draft and enqueues through the GitHub App's GraphQL
`enqueuePullRequest`. Merge is enabled only when the PR is open, not a draft,
mergeable, and its latest CI run concluded `success`, and merges through the
REST endpoint with the method the repository allows. The server acts with the
App's installation token, records `pr.merge_requested` with actor, PR and
outcome before calling GitHub, and passes GitHub's refusal back as a 409. The
position line changes on the following stream event; the reply itself changes
nothing on the page.

### 15.4 Write safety

Every write is a `POST` under `/projects/{id}/progress/` and obeys all of:

1. No write on GET; the router refuses other methods.
2. Same-origin, strictly: the `sameOriginForm` check plus refusing
   `Sec-Fetch-Site: none`, since a script-issued fetch is never a user-typed
   navigation (form routes keep accepting `none` for bookmarks).
3. The page script sends `X-Requested-With: lode-cockpit` and the route
   requires it; a form cannot set it and a cross-origin script triggers a
   preflight the server never answers.
4. JSON body, JSON reply: 200 with the new fact's id, 403 when the actor may
   not act, 409 with a reason when the backbone refuses, 422 malformed, 415 for
   a form-encoded body. Never HTML.
5. Guarded in `routeGuards`; the owner gate for acceptance stays in the store.
6. The actor is the session subject; a body naming an actor is malformed.
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

### 15.5 Live updates

`GET /projects/{id}/progress/events` is a Server-Sent Events stream shaped like
`GET /api/v1/events/stream` (a poll over the events log with `Last-Event-ID`
resume and a 30-second heartbeat), web-session guarded and filtered to one
project. The server resolves each event's task or document id to the project,
plan and specs once, on the way out, and emits one frame per event touching the
project.

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
/projects/{id}/progress/summary`. Expansion state, a pinned tooltip and an
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

### 15.6 Facts the page needs

- `pull_requests.queued_at`, set when a `merge_group` event with action
  `checks_requested` names the PR's head and cleared when the PR merges or the
  group is destroyed; ingested through the signed-webhook path so `lode inbox
  import` replays it.
- A per-repository, per-base-branch `merge_queue boolean`, read through the
  App (`GET /repos/{owner}/{repo}/rules/branches/{branch}`, rule type
  `merge_queue`) when a repository is first mapped, refreshed on the
  `repository_ruleset` webhook and once a day. The page never asks GitHub on a
  request.
- `tasks.plan_doc` is the only link from a task to its plan. The page never
  infers links from task text.

### 15.7 Assembly and routes

`internal/store/docplanning.go` `ProjectProgress(ctx, project)` returns one
`model.ProjectProgress`, reusing the covers and minted-task readers behind
`NeedsPlanning` and `NeedsExecution` and the run-board readers for leases, PRs
and CI runs. `internal/progress` is the pure derivation. `internal/api/progress.go`
holds the handlers; `internal/ui/progress.templ` renders page and fragments from
`ui.ProgressView`; `internal/ui/assets/progress.js` holds the two-step buttons,
stream client, fragment swap and tooltips under `script-src 'self'`.

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
| `POST /projects/{id}/progress/review` | as §13 defines |

Metrics: a counter of stream frames by event type, a gauge of open stream
connections, a counter of write outcomes by route and status.

## 16. Drift and overview

Intent is declared, reality is observed, and every gap between them is a query
over the diff. Two edge layers sit over the same node set (Components,
DesignDocs, Tasks, Deliverables): the **declared** layer, authored with a design
document and counting as intent once its review is resolved, and the
**observed** layer, derived mechanically and overwritten on every run.
**Drift is the set difference between the layers, per predicate.** The graph
model, IRI grammar and `wl:` vocabulary are in
07-knowledge-graph-and-search.md.

### 16.1 Named graphs and derivers

| Named graph | Layer | Writer |
|---|---|---|
| `…/graph/declared/<designdoc-id>` | declared | design-authoring skill, one per design doc |
| `…/graph/observed/go-imports/<host>/<owner>/<repo>` | observed | `lode graph derive`, per repo |
| `…/graph/observed/repo-layout/<host>/<owner>/<repo>` | observed | `lode graph derive`, per repo |
| `…/graph/observed/repo-implements/<host>/<owner>/<repo>` | observed | `lode graph derive`, per repo |
| `…/graph/observed/pr-affects` | observed | server-side, org-global |
| `…/graph/observed/deploy` | observed | server-side projection from `internal/hooks/` |

One writer per graph: a deriver's contract is a blind whole-graph `PUT`, so the
graph name encodes everything that distinguishes independent writers. Repo-local
sources run from each repo's checkout and partition per source per repo; their
subjects are repo-owned, so sibling graphs union cleanly and queries read the
family by IRI prefix. Backbone-derived sources are one org-global graph each.
Two runs racing on one graph are last-write-wins and self-heal on the next run. A deriver confines its writes to its
own `observed/*` graph so a bad run never corrupts declared intent.

Every deriver is idempotent and full-replace, deterministic, cheap to re-run
(scheduled or hook-triggered, with an input content hash short-circuiting a
no-op `PUT`), and confined.

| Deriver | Input | Output |
|---|---|---|
| Go imports | `go list -deps -json ./...` plus `go.mod`; other stacks' manifests | cross-component `<A> dct:requires <B>`; intra-component edges dropped |
| Repo layout | filesystem plus `.worklode/components.yaml` (path globs to Component IRIs, first match wins) | `<repo> dct:hasPart <component>`; `<repo> wl:unmatchedPath "<prefix>"` per uncovered top-level path; asserts each component's `rdf:type` |
| Repo implements | `.worklode/implements.yaml` resolved through `components.yaml` | `<component> wl:implements <section>` |
| PR affects | ingested PR changed-file lists, joined PR to Task through the mirrored GitHub Issue (`Closes #N`) | `<task> wl:affects <component>`; the edge only and leaves the component's `rdf:type` to the repo-layout deriver, so a repo whose CI never derives fails `wl:affectsShape` deliberately |
| Deploy | Flux and GitHub hook tables | observed Artifact, Deployment, Environment nodes and edges to Deliverables; v1 projects what hooks record and does not probe prod |

The components manifest is the single place component boundaries are declared
and the path-to-component index every other deriver uses.

### 16.2 Standing queries

- **Architectural drift**, both directions. Violation: an observed
  `dct:requires` absent from declared, minus un-expired `wl:AcceptedDeviation`
  nodes (`observed − declared − acknowledged`; a deviation names the edge by
  reification without asserting it, and re-surfaces when `dct:valid` passes).
  Stale intent: a declared `dct:requires` absent from observed.
- **Doc gaps**: a `wl:Component` with no `wl:DesignDoc` governing it, plus
  unmatched repo paths.
- **Unimplemented specs**: a `wl:Section` with status `accepted` that no
  Component `wl:implements`.
- **Stale claims**: an implementation claim pinned at `vN` whose section has
  since been revised; plus orphaned claims.
- **Ready frontier**: tasks `ready` with no unresolved `dct:requires` or
  `blocks` predecessor. The authoritative frontier is computed on the backbone
  for the atomic claim (03-tasks-and-execution.md); this is the read-only
  mirror, presented pre-sorted by the same key `(is_critical, concern_rank,
  priority, blocking_fan_out)`.

### 16.3 Critical path

Estimate-free. Over the combined DAG of `dct:requires` (graph) plus `blocks`
(backbone), joined and topologically sorted on each read: `depth(t)` is the
longest unit-weight predecessor chain ending at `t`, historical, closed
predecessors included; `is_critical(t)` and the reported path are computed over
the open subgraph only, so a finished chain is never reported as holding the
project up; `fanout(t)` counts open tasks transitively blocked by `t`, walking
through closed intermediates. Closed is the per-repo `done_state` predicate;
tombstoned tasks are excluded from every measure. A cycle is a data error:
detected, excluded, and surfaced as its own finding. Everything is a query
recomputed on read; nothing is materialized as a stored `is_critical` or
`fanout` attribute. Weighted paths are v2.

### 16.4 Surface

All reads; nothing mutates the graph. Every command emits deterministic
`--json`. Spellings follow 09-cli-and-skills.md.

| Command | Returns |
|---|---|
| `lode project overview` (shortcut `lode overview`) | counts per drift class plus the critical-path head |
| `lode graph drift [--component <c>] [--acknowledged] [--docs]` | violations and stale intent; `--acknowledged` lists deviations active and expired; `--docs` adds stale and orphaned implementation claims |
| `lode graph gaps` | doc gaps and unmatched-path gaps |
| `lode graph derive` | runs the repo-local derivers from a checkout |
| `lode graph drift --docs`, `lode graph gaps --unimplemented` | the stale-claim and unimplemented-section queries |
| `lode task frontier [--project <p>]` | the ready frontier, pre-sorted |
| `lode task critical-path [--task <t>]` | path, `depth`, `fanout`; flags cycles |

The read-only web views, backed by the SPARQL endpoint: a drift board
(violations and stale intent), a doc-gap list, a spec status view (drifted,
unimplemented, accepted), a critical-path view, and the ready frontier.
Per-section coverage badges and version history join the document view. The
only ways to change the graph are authoring design and running derivers.

## 17. Non-goals

Arbitrary dashboards or workflows, sprint or cycle machinery, embedded
replacements for GitHub, Payload or Kubernetes, a mandatory graph view,
production alerting, a general organization chatbot, a project-completion
percentage or health score in any form, per-project panel choice or ordering,
CI logs or re-run controls, cross-project roll-ups on the Progress page, more
than one active rally, and editing documents or task state from the Progress
page.

## Sources

WL-SPEC-32, WL-SPEC-56, WL-SPEC-59, WL-SPEC-60, WL-SPEC-65, WL-SPEC-66,
WL-SPEC-7.

## Open questions

- 059's "round submitted by the author" and `review_rounds` rows (reviewer
  verdict per Send) use one word for two acts; the author's act is presumably
  a `base_ref` bump on `reviews`.
- 066 §2.4's hover tooltips give WCAG 1.4.13 a subject the 032 §10 audit said
  it had none; the tooltips need re-verification.
