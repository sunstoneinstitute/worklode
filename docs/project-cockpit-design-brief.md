# Worklode project cockpit — design brief

Design baseline, 2026-08-09. This brief consolidates the
[GUI research](research/worklode-coordination-gui.md) and the settled product
decisions from the cockpit grilling session. It explains the experience we
intend to build; [spec 029](specs/029-research-work-in-the-backbone.md) owns the
research-work facts and [spec 032](specs/032-project-cockpit.md) owns the durable
UI contract.

## Product thesis

Worklode is Sunstone's evidence-backed project cockpit: the place where a person
can understand what a project is trying to achieve, what needs judgment next,
who is accountable, what people and agents are doing, and which observed facts
show that work has reached review, publication, staging, or production.

It is not a general-purpose project-management workspace. It coordinates the
specific combination Sunstone runs: journalism, data science, software and data
engineering, AI engineering, and operations. GitHub, Payload, Kubernetes,
Google Workspace, and 1Password remain the specialist tools where their native
work happens. Worklode connects their evidence to the governed project.

The first release must be genuinely comfortable for journalists and editors.
Branches, leases, CI, lineage, and deployments remain available, but they are
not the vocabulary a nontechnical person must learn to answer “what happens
next?”

## The jobs the first release must do

1. Carry an idea from minimal capture through Selection and Editorial
   Evaluation into an approved project without losing its evidence or decision
   history.
2. Define data-science outputs clearly and make code, methodology, and report
   review separate, trustworthy decisions.
3. Let a human lead supervise bounded agent work, leave it running overnight,
   and return to a concise account of outcomes and exceptions.

Everything else is secondary. In particular, the MVP does not need arbitrary
dashboards, configurable workflow builders, sprints, a general chat home page,
or embedded clones of GitHub, Payload, and Kubernetes.

## Design principles

### One cockpit, several lifecycle modes

There is one project cockpit, not three competing layouts. Its stable shell
stays recognizable while its main canvas and decision rail adapt to the
project's governed state:

| Mode | When it appears | What it optimizes for |
| --- | --- | --- |
| **Editorial decision** (prototype C) | The candidate still belongs to the Intake pipeline | Reading the Selection dossier, auditing the AI recommendation, and recording the Editor and Science Lead decisions |
| **Approved launch** (prototype A) | Both roles approved and the project was created, but the lead has not entered Research | Confirming purpose, Crew, outputs, review requirements, repositories, and automation authority without inventing a “Research setup” stage |
| **Research operations** (prototype B) | The lead explicitly enters Research | Focus, governed deliverables, active human and agent work, reviews, exceptions, and the next decision through Research, Report, Story, and Distribution |

The intake candidate is technically still an intake task in the first mode. The
mode nevertheless uses the cockpit's visual language so promotion feels like a
continuation, not a move into a different product. Promotion atomically creates
the project, links it to the candidate, and redirects to approved launch.

### Stable navigation, adaptive content

Global navigation is a horizontal top bar. It contains **Home, Intake,
Projects, Work, Reviews, Deliveries, and Knowledge**. A project then gets its
own left sidebar for **Overview, Crew, Work, Deliverables, Reviews, Decisions,
Documents, and Activity**. Counts appear only where they communicate actionable
work.

The central canvas answers the project's current question. A right rail holds
one primary decision and the minimum context needed to take it. Past dossiers,
approvals, launch records, and transitions live under Decisions or Documents;
they do not survive as obsolete mode tabs.

### One explainable next decision

The cockpit names one next decision, the person or role accountable for it,
the facts it rests on, and what each available action will do. Other concerns
remain accessible but do not compete as equal calls to action. A project lead
may pin any governed object as the team's current focus and add a short note.

There is no project-completion percentage. Progress is expressed through
concrete governed objects: decisions made, deliverables and their evidence,
reviews outstanding, blocked work, and observed delivery facts.

### Human accountability and delegated AI are different concepts

People own work and decisions. Agents may be delegated execution, hold leases,
and act under bounded authority, but they never replace the accountable human
or join the Crew. The UI says, for example, “Ingerid owns this; Worklode, on
behalf of Ingerid, delegated it to the research agent pool.”

An automated action always exposes the authorizing user, effective policy
version, triggering event, resulting object or action, and durable event trail.
The interface summarizes agent activity through outcomes, state, cost, and
exceptions rather than private reasoning or a transcript wall.

### Evidence before status theatre

Every significant claim is distinguishable as declared intent, a user-reported
fact, a system-observed fact, or an AI recommendation. Recommendations show a
short explanation first, then claims and sources, then the immutable run record.
Approvals bind to exact governed revisions or commits; a later revision cannot
silently inherit an earlier decision.

## Core experience

### Home and the Morning Brief

Home is the global landing surface. On the user's next visit it presents a
Morning Brief grouped by project or pinned focus:

1. decisions and exceptions requiring that user;
2. material outcomes since their previous visit;
3. agent runs that stopped, exhausted a bound, or need judgment; and
4. routine successful activity collapsed into a compact summary.

Unresolved decisions remain visible. Opening Home does not consume the handoff;
an explicit **Reviewed through now** action advances the user's brief boundary.
Unresolved decisions persist after that boundary and routine updates collapse.
Worklode does not wake people for anything it orchestrates; production alerting
belongs to the separate operational alert system.

### Intake and Selection

Capture asks only for a title and description. A person may create the pitch,
or an AI Threat↔Intervention analysis may group and deduplicate related findings
into an unowned candidate. An AI-originated candidate must be adopted by a
person before Selection can begin.

Selection uses a versioned dossier rather than a loose stack of task comments.
AI performs the pre-research legwork and produces auditable recommendations,
but people make two explicit decisions:

1. accept Gate 1 and authorize a bounded pre-research run; and
2. accept, narrow, park, or stop at Gate 2 after reviewing its result.

Editorial Evaluation then records separate Editor and Science Lead decisions
on the exact dossier version. Approval by both promotes the candidate. A
rejection blocks promotion but leaves the dossier open for revision,
reconsideration, parking, or closure. A human override of the AI recommendation
needs a rationale and both gatekeeping roles' approval.

### Approved launch

Promotion creates a project and opens the launch mode. It shows:

- the accepted purpose and bounded research question;
- the project lead and initial Crew;
- declared deliverables and their review requirements;
- linked repositories and working documents;
- the selected AI operating policy; and
- incomplete configuration, each attached only to the capability it blocks.

The lead makes the explicit **Enter Research** decision. Open configuration does
not manufacture another lifecycle stage and need not block entry when the
dependent capability can safely wait.

### Research operations

The operations mode persists for the rest of the project. Its contents adapt as
the primary Sunstone stage moves through Research, Report, Story + Some, and
External Distribution. Adjacent-stage work may overlap; the primary stage is an
orientation, not a rule that hides legitimate work.

For later stage changes, Worklode recommends a transition from governed
milestones, deliverables, and reviews; the project lead confirms it and records
a reason when advancing with open work. Unfinished work stays attached to its
original milestone and appears as carryover. Returning to an earlier stage
appends a reasoned transition without leaving the operations cockpit or erasing
history. Once required deliverables are terminal, Worklode may recommend
closure, but the lead closes the project only after reviewing unfinished work.

The main canvas prioritizes the pinned focus, the definition of done derived
from governed deliverables, current human and agent work, and review readiness.
The right rail carries the next decision, the highest-signal exception, and
the effective automation boundary. Engineering projects use the same shell
with software delivery evidence and appropriate stage language.

## Crew and accountability

**Crew** is the user-facing name for the time-limited group of humans actively
working on a story project. The stored model remains role-labelled project
participants. One person may carry several project role labels, while the
project has exactly one accountable lead. Agents are delegates and are never
Crew members.

Approvers and reviewers appear in the Crew only when they also contribute to
the project. Any Crew member may maintain ordinary membership, with every
change recorded. Lead handoff requires the outgoing and incoming leads to
accept; if the outgoing lead is unavailable, the Editor and Science Lead may
jointly authorize it. An invited external expert may be shown before a Keycloak
identity exists, but cannot own work or approve until linked. The roster closes
with the project and remains part of its history.

Removing a member first shows every open task, decision, and review they own and
requires reassignment or explicit unassignment. Their historical contribution
remains. Linking an invited expert to a later Keycloak identity likewise
preserves the invitation, roles, and activity as one provenance chain.

## Deliverables and reviews

The default story-project template declares five outputs:

| Deliverable | Definition exposed in the cockpit | Default review/evidence |
| --- | --- | --- |
| Dataset or data product | The governed data used or produced | snapshot and lineage, schema, quality checks, Science Lead requirement where configured |
| Reproducible analysis | The code and environment that produce the findings | exact repository and commit, environment lock, entry point, tests/CI, dataset snapshots and lineage, generated outputs, prior-review diff, and qualified peer review |
| Methodology | The defensible account of sources, transformations, assumptions, uncertainty, and limitations | Science Lead and domain-expert review of an exact revision |
| Scientific report | The findings and their evidentiary argument | buddy, expert, and journalist review of an exact revision |
| Story | The publication derived from the report | Payload state, canonical URL, and configured editorial approvals |

A custom deliverable has a name, a description, and an optional URL. Its state
and review requirements still come from governed objects rather than a manually
maintained checklist.

Code, methodology, and report decisions remain independent even when a reviewer
handles them in one session. GitHub remains the native PR-review surface;
Worklode ingests and explains its evidence. Methodology and report review use
Worklode's approval-oriented view with the exact revision, supporting objects,
comments, and independent approve/request-changes actions.

Tasks do not require review by default. When a person chooses to review a plan,
the same interaction may offer review of its task results, usually the resulting
PRs, without making that requirement universal. A material revision reopens the
changed object and prompts an impact decision for dependants; it does not erase
unrelated approvals.

The analysis-level reviewer is a qualified data-science or engineering peer who
is not the author, selected through the project's reviewer template. An approval
stays valid only for its exact designated commit: unrelated repository activity
does nothing, while designating a newer commit creates a new unreviewed candidate.

Methodology revisions reference exact analysis and dataset revisions. Report
revisions reference exact methodology, analysis, and evidence revisions. The
review UI presents that chain as a graph. When an upstream revision changes, the
downstream owner writes an impact note and a qualified prior approver either
confirms the old decision still holds or reopens it.

Self-review remains disabled by default. An exception is valid only when the
effective review policy permits one and a different authorized person approves
it before the review; the policy allowance and authorization are both events.

## Automation policy and unattended work

Automation authority belongs to the object a user creates and can be changed
later. The server ships several named presets from instance configuration and
lets users choose freely:

- **Manual** — agents start only when a person delegates work.
- **Planning assist** — after a spec has been accepted and planning is
  explicitly requested, a planning agent may draft plans and stop for review.
- **Execute accepted plans** — eligible tasks from human-accepted plans may be
  dispatched as dependencies clear.
- **Bounded autopilot** — dispatch and bounded retry/fixer behavior continue
  within explicit project, repository, kind, budget, concurrency, environment,
  and expiry limits.

Specs are always reviewed. Planning never starts before spec acceptance. Plans
and task sets are optional, but if a plan is accepted its declared tasks are
minted as the backbone requires. A review session may present a spec and its
already-prepared plans together while recording separate decisions, reducing
interaction cycles without merging the gates.

The three automation hand-offs remain visible and independently understandable:

| Event | Backbone consequence | User-controlled automation |
| --- | --- | --- |
| A spec is accepted | The required planning-decision task asks whether and how to plan; “no plan” is an explicit answer | Leave it for a person or delegate it to a planning agent |
| A planning agent finishes | Draft plan(s) are available but unaccepted | Prepare a combined review view and wait for human decisions |
| A plan is accepted | Its declared execution tasks are minted | Leave them ready, suggest delegates, or dispatch eligible tasks automatically |

This distinction lets users choose the level of AI help without making document
acceptance, plan acceptance, or review into an agent side effect.

The unattended-run contract shows scope, effective policy, agent pools,
eligible work, concurrency, token/spend budget, expiry, stop conditions,
environment boundaries, secret readiness, and accountable user before start.
During execution it groups work into ready, running, waiting, needs judgment,
failed, and completed. Deterministic infrastructure/tool failures may retry
within a bound; ambiguity or semantic judgment pauses only the affected branch.
Independent authorized work continues.

## Language, accessibility, and visual character

Primary labels use ordinary verbs: **Request review, Needs changes, Enter
Research, Published, Waiting on data**. Internal terms such as lease, worktree,
Flux, or `in_review` live in expandable evidence.

The interface follows WCAG 2.2, works without drag-and-drop, exposes keyboard
focus, never relies on colour alone, and keeps object pages addressable through
stable URLs and browser history. Dense evidence is desktop-first, while Home,
intake decisions, project overview, review, and approvals must remain usable on
a Chromebook and at mobile width.

The visual system uses Sunstone's approved palette and typography: Dark Blue,
Cool Grey Light, white, Logo Yellow for primary attention, teal/cyan links,
DM Sans for interface text, and Source Serif 4 for project and dossier headings.
Light and dark modes respect the official contrast-safe pairings. The tone is an
editorial/scientific desk with operational depth—not a generic SaaS board.

## First interactive release

The MVP is complete when one demonstrable path can:

1. capture or adopt a pitch;
2. perform and audit Selection pre-research;
3. record the Editor and Science Lead decisions and promote atomically;
4. configure Crew, outputs, reviews, and AI authority, then enter Research;
5. bind dataset, analysis, methodology, and report evidence to exact revisions;
6. run accepted work within an unattended-run contract; and
7. return to a sourced Morning Brief with remaining human decisions obvious.

The current read-only prototypes remain disposable exploration aids:

- Intake: `/?prototype=intake&variant=A|B|C`
- Cockpit modes: `/projects/cockpit-preview?variant=C|A|B`

They communicate the interaction hierarchy, not implementation architecture or
final component styling.
