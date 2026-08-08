---
status: draft
requires:
  - 005-prioritization-and-pickup.md
  - 011-delivery-lifecycle.md
  - 017-task-secrets.md
  - 023-keycloak-primary-auth.md
  - 024-multi-harness-integration.md
  - 025-documents-in-the-backbone.md
  - 028-escalation-and-document-lifecycle.md
  - 029-research-work-in-the-backbone.md
---
# Spec 032 — Project cockpit

## 0. Purpose and boundary {#sec-0}

Worklode needs one human-facing surface that makes its execution backbone useful
to journalists, editors, data scientists, engineers, and people supervising AI
agents. That surface is the **project cockpit**: a stable application shell whose
project canvas adapts to the governed state of the work.

This spec owns the information architecture, lifecycle modes, primary actions,
evidence disclosure, and first interactive release. It does not redefine the
entities it presents. Research projects, intake, Crew membership, milestones,
deliverables, and approvals come from 029; documents and plans come from 025;
tasks, agent sessions, delivery facts, and escalation retain their existing
owners.

The product and visual rationale is recorded in
`docs/project-cockpit-design-brief.md`. This spec states only behavior that must
remain true after the first implementation.

## 1. Facts drive the interface {#sec-1}

The cockpit is a projection over governed objects and events. It stores no
parallel completion percentage, editable project health, manually advanced
workflow column, or duplicated delivery/publication status.

Every displayed status retains its evidence category:

- **declared** — intended scope, requirement, owner, deliverable, or policy;
- **user-reported** — an authenticated person's explicit report or decision;
- **observed** — a fact ingested from GitHub, CI, Payload, a data pipeline,
  Kubernetes/Flux, or another emitter/prober; or
- **recommended** — an AI-produced interpretation that remains auditable and
  does not itself satisfy a human gate.

The outcome layer presents purpose, accountability, next decision, blockers,
deliverables, and approvals in plain language. The work layer adds tasks,
documents, dependencies, branches, PRs, datasets, and review threads. The
evidence layer adds exact revisions, events, agent sessions, costs, lineage,
deploy/runtime facts, and correlation gaps. These are progressively disclosed
views of the same facts, not separate products or permission boundaries.

## 2. Application and project navigation {#sec-2}

The global application navigation is a horizontal top bar with these primary
destinations:

1. **Home** — personal work, decisions, supervised agents, and the Morning Brief;
2. **Intake** — capture and the Discovery-to-Editorial-Evaluation pipeline;
3. **Projects** — the cross-project portfolio;
4. **Work** — task-oriented saved queries and the ready frontier;
5. **Reviews** — decisions awaiting the current actor;
6. **Deliveries** — publication, deployment, and operational evidence; and
7. **Knowledge** — documents and graph-backed expert views.

When a project or candidate dossier is open, its left sidebar provides local
navigation for Overview, Crew, Work, Deliverables, Reviews, Decisions, Documents,
and Activity. Global and local navigation must remain visually and semantically
distinct. The selected object has one canonical URL; browser back/forward and
copying that URL must work across full-page and narrow layouts.

## 3. One cockpit with lifecycle modes {#sec-3}

The project route lands on the cockpit Overview. The shell and local navigation
remain stable; the center canvas and decision rail select one of three modes from
governed facts:

| Mode | Selection rule | Primary job |
| --- | --- | --- |
| **Editorial decision** | The object is an intake candidate that has not passed both Editorial Evaluation decisions | Read the dossier, audit the recommendation, and decide |
| **Approved launch** | Promotion created the project and no explicit Enter Research decision exists | Confirm accountability, outputs, review requirements, working surfaces, and automation authority |
| **Operations** | The project lead entered Research, or the project is not an intake-promoted investigation | Coordinate focus, deliverables, reviews, delivery evidence, people, and agents |

The prototype labels C, A, and B respectively name these modes only; they are
not persisted variants or user preferences. Promotion switches Editorial
decision to Approved launch atomically. The project lead's explicit Enter
Research decision switches Approved launch to Operations. Operations persists
through Research, Report, Story + Some, and External Distribution while its
content and stage orientation adapt.

There is no Research setup stage. Incomplete launch configuration blocks only
the capability that depends on it. The lead may enter Research with noncritical
items deferred when the decision surface states the effect.

The primary Sunstone stage is derived from governed decisions and work. It is an
orientation, not an exclusivity constraint: adjacent-stage work can overlap and
remains visible. The earlier dossier, approvals, promotion record, and launch
configuration remain under Decisions and Documents instead of surviving as old
mode tabs.

After Research, the decision rail presents evidence-backed stage recommendations,
not automatic transitions. The project lead confirms the transition and supplies a
reason when earlier work remains open. That work appears as carryover under its
original milestone. Re-entering an earlier stage appends a transition with a reason,
preserves history, and keeps the Operations cockpit. When required deliverables are
terminal, the rail may recommend closure; the lead reviews unfinished work and
explicitly closes the project and its active Crew.

## 4. Focus and the next decision {#sec-4}

Every cockpit Overview presents at most one primary **next decision**. It names:

- the decision in ordinary language;
- the accountable actor or role;
- the exact governed revision or facts being decided;
- why the decision is ready or blocked;
- what each permitted action changes; and
- the material evidence and contrary evidence available for inspection.

Other concerns remain visible in a secondary list. They do not receive equal
visual weight or create a dashboard of competing alerts. A project lead may pin
any governed project object as the current focus with a short note; pinning
changes orientation and ranking display, not the object's state or authority.

The cockpit never displays a project-completion percentage. Progress is the
state of the decisions, deliverables, reviews, dependencies, and observed facts
that define the project.

## 5. Intake and promotion {#sec-5}

Intake capture requires only a title and description. The intake portfolio may
also show deduplicated, AI-originated candidates, but a named person must adopt
one before Selection begins. The detailed candidate view uses the cockpit shell
and Editorial decision mode even though the governed object remains the intake
task until promotion.

Selection presents a versioned dossier with the current question, strongest
evidence, evidence against the pitch, unknowns, hypothesis changes, source and
claim links, and the AI recommendation. Its audit path is layered: concise
recommendation, claims/evidence, then immutable run record including effective
policy.

The interface records the two Selection decisions separately: authorize bounded
pre-research after Gate 1, then accept/narrow/park/stop after Gate 2. Editorial
Evaluation records separate Editor and Science Lead decisions on the exact
dossier revision. It offers no generic “Create project” action: once both
decisions approve, 029 §8.1's promotion transaction runs and redirects to the
created project's Approved launch cockpit.

Rejection by either gatekeeping role prevents promotion but leaves the candidate
open. The rejecting role is shown as accountable for choosing revision,
reconsideration, parking, or closure. Overriding an AI recommendation requires
a visible rationale and the joint approval defined by 029.

## 6. People and agent presentation {#sec-6}

The project uses **Crew** everywhere people see the time-limited group defined by
029 §6.1. It does not expose `participants` as primary UI language. Each person
may show several project role labels, while exactly one project lead is visually
distinct as the accountable human.

An agent is shown as a delegate on work, never as a Crew member or substitute
owner. Where an automatic action appears in activity, the actor label is
**“Worklode, on behalf of _User_”**, linked to the effective authorization and
event. An approver or reviewer appears in Crew only when the participant facts
say they actively contribute to the project.

The UI may show an invited external expert before identity linkage, but disables
ownership and approval actions until the invitation is linked to a Keycloak
actor. Crew changes and lead handoffs use 029's event-backed rules and remain
visible in project history.

Removing a Crew member opens a responsibility review listing their open tasks,
decisions, and reviews. Removal completes only after each is reassigned or
explicitly unassigned. Past roles and contributions remain visible. Linking an
external invitation to a Keycloak actor updates the same displayed person and
preserves the invitation and activity history.

## 7. Deliverables and review {#sec-7}

The Overview derives a **definition of done** from deliverable objects and their
required evidence and approvals. It does not maintain a checklist separate from
those objects. A custom deliverable editor asks for name, description, and an
optional URL.

Dataset/data product, reproducible analysis, methodology, scientific report,
and story remain distinct governed targets. The cockpit shows their individual
readiness and relationships. In particular, it must not collapse these three
decisions:

1. code and analysis evidence at an exact commit;
2. methodology review at an exact document revision; and
3. report review at an exact document revision.

GitHub remains the primary interaction surface for PR review. Worklode shows the
normalized review/check evidence and a source link. Methodology and report use
an approval-oriented Worklode view with the submitted revision, supporting
deliverables, comments, assigned qualified reviewers, and independent Approve
or Request changes actions.

The analysis review presents the exact repository and commit, environment lock,
entry point, tests/CI, dataset snapshots and lineage, generated outputs, and the
diff from the previously reviewed revision. Its required reviewer is a qualified
data-science or engineering peer who is not the author. When the deliverable
designates a newer commit, the previous approval stays attached to its exact
revision and the newer candidate is visibly unreviewed.

The methodology view binds exact analysis and dataset revisions; the report view
binds exact methodology, analysis, and evidence revisions. Both expose this as a
review graph. For a possible downstream impact, the owner submits an impact note
and a qualified prior approver confirms the existing approval or reopens it. A
self-review exception is available only when policy permits it and a different
authorized actor approves it before review; both facts appear beside the decision.

One review session may present several targets, including a spec and prepared
plans, but it records a separate decision for each. Specs are always reviewed;
plans and task-result review are optional. When a user elects to review plans,
the session may offer review of resulting task outputs, normally PRs. Tasks do
not acquire a review requirement merely because they exist.

A material change reopens the changed target and exposes an impact decision for
dependent targets. Unrelated approvals remain intact. Approval displays always
name the exact version, actor, role, time, source, and any policy exception.

## 8. Automation and unattended execution {#sec-8}

Automation appears as scoped authority on the governed object, not as an
assignee. Its control shows the effective rule in verbs and previews what the
next relevant event will do. Instance configuration supplies named presets;
users may choose among them and may change a policy later. Every automatic
action records the authorizing actor and effective policy version.

The default presets are Manual, Planning assist, Execute accepted plans, and
Bounded autopilot. The surface preserves these invariants:

- no planning agent starts before the governing spec is accepted;
- spec acceptance remains an explicit human decision;
- plan review/acceptance remains a separate decision when a plan is used;
- accepting a plan mints exactly its declared tasks as 025 requires; and
- automatic execution acts only on eligible ready tasks within the saved bounds.

The policy preview exposes the three hand-offs separately. On spec acceptance,
027 mints the planning-decision task; policy decides whether it waits for a
person or is delegated to a planning agent, and “no plan” remains an explicit
human outcome. A planning agent may produce draft plans but never accept them.
On plan acceptance, 025 mints the declared execution tasks; policy decides
whether they stay ready, receive suggested delegates, or dispatch automatically
as they become eligible. Combining several targets in one review surface does
not combine these decisions or their event records.

Before an unattended run starts, one confirmation surface shows its projects
and repositories, eligible kinds, agent pools, concurrency, token/spend budget,
expiry, retry and stop conditions, environment authority, 1Password readiness
using symbolic requirement names and readiness only (never values or `op://`
references), accountable user, and human gates that remain.

The live run groups work into Ready, Running, Waiting, Needs judgment, Failed,
and Completed. Each active item shows its owner, delegate, lease age, last
durable event, cost, PR/check state, and next expected signal. Deterministic
tool or infrastructure failures may retry within policy; ambiguous or semantic
failures pause the affected dependency branch. Independent authorized branches
continue. Pause automation stops new dispatch without rewriting task state.

## 9. Home and the Morning Brief {#sec-9}

Home is the default post-login destination. It combines assigned human work,
approvals, agent work the actor supervises, and a Morning Brief computed for
that actor from governed events since their previous visit.

The brief groups by project or pinned focus and orders content as follows:

1. decisions and exceptions needing the actor;
2. material outcomes and changes;
3. runs that stopped or reached a bound; and
4. routine successful work, collapsed.

Unresolved items persist across briefs. Worklode schedules no wake-up, email,
chat, or off-hours notification for orchestrated work in the MVP. Production
alerts are out of scope and remain in the operational alerting system.

Opening Home never marks the brief consumed. An explicit **Reviewed through now**
action advances the actor's event boundary to the displayed cutoff. Unresolved
decisions remain in subsequent briefs; routine updates at or before the cutoff
collapse.

## 10. Accessibility and responsive behavior {#sec-10}

The first implementation targets WCAG 2.2 AA. Primary workflows must be
keyboard operable, use semantic landmarks and labelled controls, announce
asynchronous action results, provide visible focus, avoid colour-only meaning,
and meet minimum pointer-target sizing. Every drag, board, or timeline action
has a form or menu equivalent.

Home, Intake decisions, project Overview, review, and approval work at narrow
browser widths and on a Chromebook. Narrow layouts use full-page detail,
progressive disclosure, and reduced columns rather than compressing the desktop
grid. Specialist evidence may remain desktop-dense but cannot be the only path
to a primary decision.

The UI follows Sunstone's approved contrast-safe palette and typography. Visual
style is not a source of domain meaning; icons, text, and state labels accompany
colour.

## 11. First interactive release {#sec-11}

The first release is three connected vertical slices:

1. Intake capture/adoption through both Selection gates and the two-role
   Editorial Evaluation promotion.
2. Project launch plus dataset, analysis, methodology, report, and story
   deliverables with code/methodology/report review lanes.
3. A bounded unattended run and the next-login Morning Brief.

Acceptance is one public-surface demonstration that follows a candidate through
all three slices. It proves that:

- the shell transitions Editorial decision → Approved launch → Operations from
  governed actions, without a variant query parameter;
- both promotion decisions and the explicit Enter Research decision are durable
  and auditable;
- the project lead, Crew, agent delegates, deliverables, and next decision are
  distinguishable without specialist vocabulary;
- review decisions bind exact targets and changed targets do not retain stale
  approval;
- source-native GitHub, Payload, pipeline, and delivery facts deep-link from a
  plain-language summary;
- an unattended run cannot exceed its authority and does not wake a person; and
- the Morning Brief makes remaining human judgment obvious without presenting
  a raw activity firehose.

As with every web path in this repository, end-to-end tests drive the HTTP UI
and API surfaces and do not write directly to the store.

## 12. Non-goals {#sec-12}

Out of scope: arbitrary dashboards or workflows, sprint/cycle machinery,
embedded replacements for GitHub/Payload/Kubernetes, a mandatory graph view,
production alerting, and a general organization chatbot.
