---
status: draft
requires:
- docs/specs/006-knowledge-graph.md
- docs/specs/004-execution-backbone.md
- docs/specs/025-documents-in-the-backbone.md
---
# Spec 029 — Research work in the backbone: projects, milestones, deliverables, approvals

## 0. Why {#sec-0}

The backbone tracks engineering execution: agents claim tasks into worktrees, webhooks
record what happened, provenance is an event log. Sunstone's actual product is made by
journalists and data scientists working the Sunstone Way from Discovery through Report,
and that work is invisible to the backbone today. Four problems, named by the CTO for
the 2026-08-11 rollout, define what this spec must fix:

1. The editor cannot learn a project's status without chasing someone.
2. Nobody can tell whether a deliverable has actually been published; the CMS and the
   data pipeline emit no events, and every hop between them is a human remembering.
3. There is no shared definition of done for data-science work, and no stated
   deliverable per project.
4. Reviews of code, prose, and CMS content leave no queryable record.

The design extends the backbone rather than forking it: research work gets the same
event log, the same project scoping, the same claim/lease machinery where agents are
involved — plus the entities engineering work never needed: milestones, deliverables,
participants, and approvals. Humans become first-class actors through assignment
(the 2026-08-06 human-assignment plan), which this spec adopts.

Scope boundary: this spec covers the Sunstone Way's Discovery through Report stages.
Story and Distribution reuse the same entities but add no new ones.

## 1. The investigation is a project {#sec-1}

Each investigation is one worklode project — its key prefixes every identifier, its
page answers "what is the status of cost-of-war", and its lifecycle is the
investigation's lifecycle.

Research projects own no git repo. The research monorepo maps once, to a standing
umbrella project, which is what opens the webhook gate (`internal/hooks` records
events only for mapped repos); investigation projects are repo-less. Scoping inside
the monorepo works per directory: each investigation directory carries
`.worklode/config.toml` with `current_project`, and the nearest-config-wins rule
resolves it. Commit, branch, and PR correlation is unaffected — a PR on
`lode/COW-7-slug` attaches to task `COW-7` regardless of which project the repo maps
to, because correlation matches on task id alone.

Projects gain three pieces of metadata:

- **`seeded_by`** — a reference to the intake task the project was promoted from
  (§8). Nullable; engineering projects have none.
- **Labels** — free-form key/value metadata, set at promotion, that classification
  rules act on (§6.2). `kind=sunstone-story` is the first label with meaning.
- **`horizon`** — `bounded` or `standing`. An investigation is bounded: it ends.
  A project holding an in-house infrastructure component is standing: it does not.

`horizon` is an attribute, not a class. 025 §13 deleted `wl:OngoingMaintenance`, and
that deletion stands — restoring it would make "unbounded" a *kind of thing* disjoint
from Project, so a standing infrastructure project could not be a Project at all, and
could not carry a project key, tasks or a focus. Being unbounded is something a
project *is*, not something it *is instead of*. The task-level reading of the same
word is separate and needs no term either: a task with no milestone is ongoing
maintenance, which is the query `milestone_id IS NULL` (§2).

The Sunstone stage is likewise a query, not an editable project-status column. It is
derived from governed decisions, milestones, deliverables and work. Entering Research
is an explicit decision by the project lead; that decision is one of the facts the
query reads. Work from adjacent stages may overlap — the derived primary stage orients
people and never hides valid work merely because it belongs to the next stage.

After Research, Worklode may recommend a stage transition from governed milestone,
deliverable and review facts; the project lead confirms it. Advancing with unfinished
work requires a reason. That work remains attached to its original milestone and is
shown as carryover — stage movement neither closes nor reparents it. Returning to an
earlier stage appends another reasoned transition event and preserves the full history.
Once all required deliverables are terminal, Worklode may recommend closure, but the
lead explicitly closes the project after reviewing every unfinished item. Closure, not
deliverable state alone, ends the bounded project and its active Crew.

## 2. Milestones and deliverables replace the epic {#sec-2}

The container hierarchy becomes:

```
project ── milestone ──┬── task ── subtask
                       └── deliverable
```

**Milestone** and **deliverable** are new entities with their own tables, not task
kinds. A deliverable cannot be claimed, worked, or closed — modelling it as a task
would store a row that lies about what it is. A milestone's progress is a query over
its tasks and the state of its deliverables; it stores no state of its own beyond
identity, title, and ordering. This preserves 025 §1's rule: groupings are queries.

- A project has one or more milestones. A milestone has zero or more tasks and zero
  or more deliverables.
- A task references its milestone through a nullable `milestone_id`. Tasks without a
  milestone are **ongoing maintenance** — legal everywhere, and the norm for
  engineering projects. The sunstone-way skill requires milestone attachment for
  research-project tasks; the server does not.
- Task → subtask survives exactly as 004 §6.10 left it, minus the epic: `decompose`
  creates parent-hood and children in one transaction, and `checkHierarchy` accepts
  an ordinary task as parent instead of requiring `kind = 'epic'`. The depth cap of
  2 edges now spans task → subtask only and stops binding in practice.
- **`epic` is removed from `TaskKind`** — convergent with 025 §10's kind list, which
  already dropped it. What 004 built the epic *for* (a declared container above
  tasks) is carried by the project and the milestone, both real objects with facts
  of their own. The migration follows the standing rule: the kind CHECK,
  `validKinds`, and `wlc:TaskKind` change together, held by the existing test.

The default shape, minted at promotion (§8) for `kind=sunstone-story` projects: two
milestones, **internal review** and **publication**, with deliverables dataset/data
product, reproducible analysis, methodology, scientific report, and story attached.
This is a starting point the team refines, never a universal schema the server
enforces. Named, versioned instance configuration supplies the template and review
flow; a project receives a snapshot so a later configuration edit cannot silently
change its definition of done.

## 3. Deliverables {#sec-3}

A deliverable is a declared, checkable output: a datapackage, a report PDF, a CMS
post, a docker image, an Iceberg table, a named graph. It answers two questions the
task set cannot: *what does this milestone ship*, and *is it actually live*.

### 3.1 Identity and the check {#sec-3.1}

A deliverable declares how its existence and state are verified:

- **By address**, when known in advance — a GCS URL, a CMS slug, a table name.
- **By label**, when the address is minted at build time (a docker tag, an Iceberg
  snapshot). Worklode defines the label key and value at deliverable creation
  (`worklode.deliverable=COW/datasets`); skills materialize the convention into
  deterministic checks — lint rules verifying `docker-bake.hcl` applies the tag, or
  that `datasets.yaml` carries the label definitions (sunstone-py grows support as
  needed). Humans never link artifacts to deliverables by hand; hand-linking is
  toil, and toil is skipped exactly when it matters.

The configured story-project deliverables above have well-known meanings, not hardcoded
rows. A person may add a custom deliverable with exactly three descriptive fields:
**name**, **description**, and an optional **URL**. Its required evidence and approvals
are separate governed rows, so adding a link never makes the output complete by itself.

### 3.2 State {#sec-3.2}

Deliverable state is **reported, never asserted by a human closing a task**:

- **Push**: emitters report over the API — CI after a prod publish, the CMS on a
  post's `_status` transition, the data pipeline on a dataset registration. Each is
  an ingest source in the 004 sense: signed or bearer-authenticated, one
  `RecordEvent` per fact, idempotent by `(source, external_id)`, which makes
  at-least-once delivery a no-op on duplicates by construction.
- **Poll**: where an emitter cannot push, a separate prober process (the
  `lode watch` pattern — own deployment, bearer token, dedupe keys) checks declared
  addresses and reports what it finds. The prober holds the read credentials, so the
  server's blast radius stays the database it owns.

"Is the project published" is then a query: every deliverable published ⇔ the
project is published. No column stores it.

Every deliverable fact retains how it became known: **observed** from an emitter or
prober, or **user-reported** by an authenticated actor where no integration exists.
The UI uses “User-reported” exactly; “human-reported” is not product language. A
user-reported fact is auditable but does not masquerade as independent verification.
Declared intent, reported/observed state, and an AI recommendation therefore remain
three different things.

Probing as *verification* of reported state — reconciling a claim against production
— stays on 007's v2 line and is out of scope here.

### 3.3 Lineage {#sec-3.3}

`wl:Deliverable` is 006's declared definition-of-done made concrete, and a
deliverable with no artifact (a state change, an effect) is 006's `wl:Effect`. The
ns/ mirrors follow at acceptance.

## 4. Identifiers {#sec-4}

Every entity kind draws from its **own per-project ordinal sequence**, allocated
from `(project, kind)` counter rows:

| Entity | Form | Sequence |
|---|---|---|
| Task, subtask | `COW-7` | shared, bare — unchanged |
| Milestone | `COW-MILE-2` | own |
| Deliverable | `COW-DEL-3` | own |
| Spec | `COW-SPEC-4` | own |
| ADR | `COW-ADR-1` | own |
| Plan | `COW-PLAN-4-1` | per parent spec |

A plan's first ordinal is its parent spec's; its second counts plans under that spec
from 1. A plan with no governing spec uses spec ordinal **0** (`COW-PLAN-0-2`) —
legal when the work is mechanical enough for the executing tier without a design
document, a judgment the planning skill makes and a human may override in either
direction. The spec+plan ceremony is deliberately skippable for simple work;
`PLAN-0` keeps the skip visible instead of laundering it.

Only tasks are claimable, so only bare `<KEY>-<n>` ids ever appear in branch names,
`WL-Task:` trailers, or merge-subject correlation — the existing patterns
(`[A-Z][A-Z0-9]*-[0-9]+`) are untouched by construction. The scheme generalizes
025 §14.3's cross-corpus shorthand (`WL-SPEC-1`), which already reads as an
instance of it. The type segment is also what `lode show <id>` dispatches on, with an
equivalent `--kind`/per-kind flag spelling for each (019 §4.4); kinds in this table
whose entities do not exist yet are recognized and reported, never treated as typos.

Documents drawing identity from these sequences changes 025's assumptions; its
implementation plans are re-planned before execution.

## 5. References across projects {#sec-5}

Containment never crosses a project boundary: a milestone's tasks and deliverables
are its own, which keeps "status of cost-of-war" a bounded query. Two references do
cross:

- **`blocks`** between tasks, as today — a story task blocked by a data-platform
  task in another project.
- **Deliverable references** — a milestone may depend on a deliverable produced
  under another project ("the story ships when that Iceberg table exists") without
  owning the work that produces it.

Plus `seeded_by` from a project to its intake task (§1). References are rows in one
typed edge table `(from_kind, from_id, to_kind, to_id, rel)`; there is no unified
entities table — per-kind tables with per-kind counters carry identity.

## 6. People {#sec-6}

### 6.1 Assignee, participants, contributors {#sec-6.1}

**Crew** is the user-facing name for the time-limited group of humans working on a
bounded story project. It is not another container or stored entity: crew membership
is the project's role-labelled participant rows below. Agents execute delegated work
but are never crew members; the project lead remains the accountable human.

- **Assignee** (one, nullable): ownership of a task or decision. Semantics as the
  2026-08-06 human-assignment plan: separate from leases, lease-free
  start/stop/submit lifecycle, auto-assign on start. One assignee — shared
  assignment dilutes the feeling of responsibility; a genuinely joint task splits.
- **Participants** (stored, per project and role-labelled): who is *on* the
  investigation, visible before any task is picked up. The UI calls this set the
  **Crew**. One actor may carry several project role labels; exactly one participant
  is the project lead. Agents, advisory-only approvers and reviewers, and notification
  recipients are not silently added to it.
- **Contributors** (derived): everyone who was ever assigned a task in the project.
  Derivation is the point — an engineer who fixed a pipeline task gets credit on the
  story without anyone maintaining a list.

Any Crew member may add or remove an ordinary Crew member; every change is an event.
Before removal, every open task, decision and review the member owns must be reassigned
or explicitly left unassigned; the member's historical roles and contribution remain.
Changing the project lead requires acceptance by the outgoing and incoming leads. If
the outgoing lead is unavailable, the Editor and Science Lead jointly authorize the
handoff. Closing the project closes the active Crew but preserves its roster, role
history and derived contributions.

An external expert may begin as an invited participant without a Keycloak actor. The
invitation may be shown in the Crew, but it cannot own work, resolve an approval, or
act as project lead until linked to an authenticated identity. Linking preserves the
invitation and participation history rather than replacing it.

### 6.2 Identity and roles {#sec-6.2}

Keycloak is the human identity (001). Two additions:

- The `githubUsername` user attribute is mapped into the token and stored on the
  actor, so GitHub facts (PR authors, reviewers, commits) attach to the person, not
  just the task.
- The groups claim is stored on the actor **in full** at login, not filtered to
  `user`/`admin`. Gates check group membership by name; Keycloak stays the sole
  authority, and hiring a Science Lead is a Keycloak change and nothing else.
  Stored groups go stale between logins — which is why approval is a web-session
  act (§7.3), never a CLI-token act.

## 7. Approvals {#sec-7}

### 7.1 One table, every approval {#sec-7.1}

A single `approvals` table serves every kind of approval — deliverables, documents,
PRs, tasks — keyed to `(entity_kind, entity_id, subject_revision)`, carrying the
required role (a Keycloak group name) or a named actor, the resolving actor,
timestamps, and state. `subject_revision` is the immutable document revision,
analysis commit, PR head, or deliverable-evidence revision the actor actually saw:

```
awaiting ──▶ changes_requested ──▶ (back to awaiting on re-request)
awaiting ──▶ approved | rejected     -- final by convention, not by constraint
```

The requirement is **materialized as an `awaiting` row when the entity is created**.
This is the design's load-bearing choice: a *missing* approval is a visible row, so
"what is waiting on whom" is a query, and an absent sign-off can never be
indistinguishable from a not-required one.

Each decision binds the exact governed revision it reviewed: a document version, an
analysis commit, a PR head, or a deliverable evidence revision. Submitting a material
change reopens the changed target; it does not erase decisions on unrelated objects.
Dependent objects receive an explicit impact review rather than automatic blanket
invalidation: the downstream owner supplies an impact note, then a qualified prior
approver confirms the existing decision still holds or reopens it. Approval by an
object's author is disallowed by default. A self-review exception is valid only when
the effective review policy allows it and a different authorized actor approves the
exception before review; the allowance and authorization are both event-logged.

### 7.2 Where requirements come from {#sec-7.2}

Rules trigger on project metadata: promotion from intake stamps the labels (§1), a
rule matching `kind=sunstone-story` sets the project's `approval_flow` reference,
and the named, versioned flow declares which entity kinds need which role's sign-off.
Flows live in instance configuration and Worklode ships pre-baked defaults. The
project stores the effective snapshot so a later config edit cannot silently change
an open review.

The default story flow keeps three review lanes independent:

- reproducible analysis: GitHub PR review per task policy plus one analysis-level
  qualified data-science or engineering peer decision on an exact commit; the peer is
  selected through the project reviewer template and is not the author;
- methodology: Science Lead and domain-expert decisions on an exact revision; and
- scientific report: buddy, expert, and journalist decisions on an exact revision.

One review session may present multiple targets, but records separate decisions.
Tasks have no review requirement by default. If a user elects to review a plan, the
same choice may require review of that plan's task results, typically their PRs.
Ad-hoc requirements can be added to any governed target. Rule-created rows are owned
by the system `worklode` actor: the rule inserted them, and attributing policy to
whichever human filed the idea would misstate who did what; the event log preserves
causality.

An analysis review submission is a revision-bound evidence bundle: exact repository
and commit, environment lock, executable entry point, tests/CI, dataset snapshots and
lineage, generated outputs, and the diff from the previously reviewed revision. An
approval stays on that exact designated commit. Later repository commits do nothing
until the deliverable designates a newer commit, at which point the new revision is an
unreviewed candidate.

Methodology revisions reference exact analysis and dataset revisions. Scientific
report revisions reference exact methodology, analysis, and supporting-evidence
revisions. Those bindings are governed references rather than links to “current”, so
the precise evidence chain an approval covered remains queryable.

### 7.3 The gates {#sec-7.3}

- **Approving is a web UI act.** The OIDC session's group claims are fresh; a
  30-day CLI token's are not (§6.2). This is the only mutation the web UI must
  learn first.
- **CI**: the manually-triggered prod publish workflow queries worklode for the
  entity's approval and fails without it — a read, no outbound machinery needed.
- **CMS**: the publish button fails unless the story's approval exists. Binding a
  Payload post to a worklode approval is **opt-in per post** — the CMS must never
  hard-depend on the tracker being up, and the check degrades to advisory when
  worklode is unreachable.
- **GitHub PRs**: the existing `pull_request_review` ingest writes into the same
  table — an `awaiting` row when a task-correlated PR opens, resolved when the
  review lands. GitHub remains the review surface (jump-out links); whether it is
  ever replaced is explicitly undecided. 025 §7.3's per-document reviewer set becomes
  rows in this table.

Specs always pass an explicit review before acceptance. Plans and execution tasks are
optional. When draft plans already exist, a spec review may present the spec and plans
in one session to reduce interaction cycles, but each retains a separate decision and
no planning action begins before the spec is accepted.

## 8. Intake, events, and notifications {#sec-8}

### 8.1 Intake {#sec-8.1}

Ideas enter a standing **intake project** at Discovery — one system of record from
the first pitch. Capture requires only a title and description. An AI
Threat↔Intervention analysis may also group and deduplicate related findings into an
unowned pitch; a named human must adopt it before Selection begins.

An idea remains a task. Selection builds a versioned dossier around it: litmus-test
results, claims, sources, unknowns, hypothesis changes, recommendation and the exact
AI run. The audit path retains the effective policy, prompts/tool calls, source
excerpts and scoring inputs while the primary view summarizes them. Two human
decisions are explicit: accept Gate 1 and authorize bounded pre-research, then decide
Gate 2 after the AI-assisted work. Starting Selection may prepare Gate 1, but no
pre-research run begins without the first authorization.

Editorial Evaluation records separate Editor and Science Lead decisions on the exact
dossier revision. Both must approve before promotion. A rejection blocks promotion
without closing the dossier; the rejecting role owns the next revise, reconsider,
park or close decision. Overriding the AI recommendation requires a rationale and
approval by both roles.

Passing Editorial Evaluation promotes the pitch without a second “Create project”
confirmation: one transaction creates the project, stamps labels and `seeded_by`,
mints the configured milestones and deliverables (§2), snapshots the approval flow
(§7.2), records the initial Crew and lead, and closes the intake task. The UI redirects
to the created project. Killed ideas cost one closed task and keep their trace.

### 8.2 Events out {#sec-8.2}

025's offset-tracked subscribers are implemented **before** any outbound consequence
— no producing handler gains a hardcoded notifier. The MVP sends no scheduled email,
Google Chat message, or off-hours notification for work Worklode orchestrates. Its
first human-facing consequence is the per-user Morning Brief in the web UI, derived
from lifecycle events when the user returns. Decisions and exceptions persist;
routine successful activity collapses. Opening Home does not advance the per-user
event boundary. The user explicitly chooses **Reviewed through now**; unresolved
decisions remain after the cursor advances. Production alerts remain a separate
system.

### 8.3 Events in {#sec-8.3}

New ingest sources (CMS publish transitions, CI publish reports, pipeline dataset
registrations) follow the 004 pattern: a new `events.source` value per system
(migration), signed or bearer-authenticated, idempotency key per fact. The CMS
additionally records *who* approved and published — the publish fact without the
person would rebuild the invisible-sign-off problem this spec exists to remove.

## 9. Out of scope {#sec-9}

- The review-surface tool (galley vs crit) — deferred until use cases crystallize;
  whichever wins must sync bidirectionally with GitHub PRs.
- Probing as verification of reported deliverable state (007 v2).
- Per-project access control. Stated assumption: every logged-in user sees every
  project, drafts included; the first genuinely sensitive investigation triggers a
  deliberate decision.
- Data-quality lint minimums for datasets — reviewer judgment first; a rule is
  written from what good looks like after a few real projects, not guessed now.
- Definition-of-done prose and task-decomposition guidance — the sunstone-way
  skill owns these; this spec only gives them entities to attach to.
- Automating the CMS datapackage import, and `report-format`'s single-report
  layout — real gaps, owned by those repos.
