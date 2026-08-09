# Worklode coordination GUI research

Research note, 2026-08-07. This is a product/UI recommendation, not a design
specification. It deliberately does not change the ownership or state semantics
established in Worklode's accepted specs.

## Question and method

What GUI should Worklode provide for a Sunstone-wide coordination layer that
spans engineering, AI agents, data science, and editorial journalism, while
following work from an early idea through Git, CI, staging, production, or
publication?

This note reads the Worklode specs and the Sunstone Way first, then compares
interaction models in GitHub, Jira, Linear, Notion, Tana, and adjacent
specialist tools. External observations below use only product-owned
documentation; every such observation has an adjacent source link.
Recommendations marked **Synthesis** are our conclusions, not claims by the
compared products.

## Constraints from Worklode

Worklode is not merely a task database. Its backbone gives a task a durable
identity, atomic claim/lease semantics, an append-only provenance log, and
dependency gates; delivery facts are ingested from GitHub and runtime/deploy
sources rather than asserted manually ([execution backbone](../specs/004-execution-backbone.md),
[delivery lifecycle](../specs/011-delivery-lifecycle.md)). The overview model
already distinguishes declared intent from mechanically observed reality and
makes drift a query rather than a hand-maintained report
([drift and overview](../specs/007-drift-and-overview.md)).

The GUI therefore needs to make four different things legible without
collapsing them:

1. Human intent: task, document, milestone, deliverable, approval, owner.
2. Agent execution: a claim, worktree, agent session, capability/tier, and
   escalation—not a fictional human assignee.
3. Observed delivery: branch, PR, checks, artifact, deployment, runtime event,
   CMS publication, or dataset registration.
4. Evidence and uncertainty: event history, drift, missing correlation, stale
   design, failed verification, and blockers.

The research-work extension makes this wider than software delivery: one
investigation is a project; milestones organize work; deliverables such as a
report, dataset, CMS post, or image are checkable objects; their state is
reported by an emitter or prober; and approvals apply to documents, tasks, PRs,
and deliverables ([research work in the backbone](../specs/029-research-work-in-the-backbone.md)).
It also requires the **crew**—the time-limited group of humans working on a
story project—to be first-class participants rather than visitors to an
engineering board. `Participant` remains the stored membership relationship;
agents are delegates, not crew members.

## Comparative pattern inventory

| Product | Source-backed interaction pattern | Fit for Worklode |
| --- | --- | --- |
| GitHub Projects | A project can expose the same items as saved table, board, and roadmap views; a view has its own filtering, grouping, sorting, and visible fields. Roadmap dragging edits dates or iteration values. [GitHub docs](https://docs.github.com/en/issues/planning-and-tracking-with-projects/customizing-views-in-your-project/changing-the-layout-of-a-view) | Strong model for one underlying work record and several role-oriented lenses. Useful for engineering execution, but GitHub's issue/PR-centric item model cannot represent Worklode's leases, observed runtime facts, or publication evidence by itself. |
| Jira | Jira separates backlog planning from an active board, and lets individuals change density, fields, quick filters, and whether details open in a sidebar. [Backlog](https://support.atlassian.com/jira-software-cloud/docs/use-your-kanban-backlog/), [board settings](https://support.atlassian.com/jira-software-cloud/docs/customize-your-view-of-the-board-and-backlog/) A work item's development panel summarizes linked branches, commits, PRs, builds, deployments, and feature flags, while approval steps let designated reviewers approve or decline in context. [Development panel](https://support.atlassian.com/jira-software-cloud/docs/view-development-information-for-an-issue/), [approvals](https://support.atlassian.com/jira-software-cloud/docs/what-are-approvals/) | Borrow the distinction between planning and active execution, plus its compact source-to-delivery evidence panel and in-context decisions. Avoid turning all work into configurable workflow columns: Worklode's state transitions and delivery facts have stronger semantics than card placement. |
| Linear | Durable filtered issue/project/initiative views can be saved and shared at workspace, team, or project scope. [Linear custom views](https://linear.app/docs/custom-views) Its project timeline intentionally shows projects, rather than granular issues, and supports milestones, dependencies, health, and several time scales. [Linear timeline](https://linear.app/docs/timeline) Most importantly for Worklode, delegating an issue to an agent leaves the human assignee responsible, and both ownership and delegation remain visible in My Issues and activity history. [Linear assignment and delegation](https://linear.app/docs/assigning-issues) | Strong model for a quiet global shell, saved operational views, a high-level project-only roadmap, and separate human accountability from agent execution. Do not import cycles/sprints: Worklode explicitly treats groupings as queries and has no sprint concept. |
| Notion | A database item is a page with rich content and properties; independent views can choose layout, visible properties, filters, sorting, and grouping. [Notion databases](https://www.notion.com/help/intro-to-databases), [views](https://www.notion.com/help/views-filters-and-sorts) Its My Tasks view aggregates assigned work from several task databases into one surface. [Notion My Tasks](https://www.notion.com/help/guides/give-your-to-dos-a-home-with-task-databases) | Borrow the detail-page-as-context model and one personal work queue across domains. Avoid a free-form schema or a workspace where authoritative state can be casually rewritten; Worklode needs typed, server-enforced transitions and auditable facts. |
| Tana | Supertags turn nodes into typed objects with field templates; items with a type can be searched, filtered, and displayed as a table. [Tana Supertags](https://outliner.tana.inc/learn/features/supertags) Search can query fields and be scoped within other nodes. [Tana search](https://outliner.tana.inc/help/search-and-finding) | Borrow fast capture plus cross-cutting search and type-aware retrieval, particularly for investigation intake and notes. Avoid exposing the ontology as end-user configuration: Sunstone needs a small, understandable vocabulary and governed integrations. |

Two cross-product conclusions follow. First, table, board, timeline, and detail
page are complementary lenses over the same facts—not competing sources of
truth. Second, planning/capture needs a low-friction entry point, while active
execution needs a constrained, trustworthy state machine. **Synthesis.**

### Adjacent specialist patterns

These products are not Worklode competitors, but their GUIs solve parts of the
same cross-functional problem.

| Product | Source-backed pattern | Lesson for Worklode |
| --- | --- | --- |
| Airtable Interfaces | A record detail interface can open as a resizable side sheet or full page, expose linked records, comments, revision history, and preconfigured actions, and render a select field as a workflow stepper for approvals or publication milestones. [Airtable record detail](https://support.airtable.com/docs/airtable-interface-layout-record-detail) | Strong precedent for giving nontechnical staff a deliberately composed interface over a richer underlying model. Borrow the review side sheet, stepper, and safe action buttons; do not ship an interface builder. |
| Backstage | The Software Catalog organizes services, sites, libraries, pipelines, and other software around discoverable owned entities, then places specialist tools around each entity through plugins. [Backstage Software Catalog](https://backstage.io/docs/features/software-catalog/) | Make the project/deliverable the integration hub. Summarize specialist evidence and deep-link to the source tool instead of embedding a miniature GitHub, Kubernetes dashboard, CMS, and data catalog. |
| Dagster | The asset catalog leads to an asset detail page with overview, partitions, events, checks, lineage, automation, and historical insight; asset checks surface data quality and freshness alongside pipeline health. [Dagster UI](https://master.dagster.dagster-docs.io/concepts/webserver/ui), [asset checks](https://master.dagster.dagster-docs.io/concepts/assets/asset-checks) | Treat datasets, reports, and other outputs as inspectable deliverables with checks and lineage, not task attachments or manually ticked boxes. Default to a compact result; keep the graph as an expert drill-down. |
| Payload CMS | Payload distinguishes Draft, Published, and Changed states when drafts are enabled; versions add history, diffs, restoration, preview, and publishing controls. Its admin views and lifecycle hooks are extensible. [Payload drafts](https://payloadcms.com/docs/versions/drafts), [versions](https://payloadcms.com/docs/versions/overview), [hooks](https://payloadcms.com/docs/hooks/overview) | Leave writing, preview, diffs, and publication in Payload. Worklode should show the linked story's reported state, approval gate, and canonical URL, and ingest lifecycle facts through hooks. |

## Sunstone-specific workflow model

The Sunstone Way has seven shared stages: **Discovery, Selection, Editorial
Evaluation, Research, Report, Story + Some, and External Distribution**. The
journalist/data-scientist pair carries most of the flow; the Editor and Science
Lead are the prioritization gate, and experts, producers, and publishers join
at defined review and distribution points. [The Sunstone Way: Our process](https://docs.google.com/document/d/13XoO-jb8yW0u_8SN1wLq6uICSxYWarA7Yb-SExM_YSs/edit)

Worklode should render those stages directly for investigations instead of
renaming them to generic software statuses. They map cleanly onto the proposed
backbone model:

| Sunstone stage | Primary Worklode object/surface | Evidence or gate the GUI should show |
| --- | --- | --- |
| Discovery | Intake task and fast capture form | Pitch, originator, source links, discussion, and whether AI contributed. |
| Selection | Intake detail / litmus-test view | Current litmus-test result, unresolved questions, and recommendation. |
| Editorial Evaluation | Decision-oriented intake queue | Journalist/data scientist recommendation; Editor and Science Lead decision; promote or close with rationale. |
| Research | Investigation project home | Crew, current milestone, work in progress, methods, source material, blockers, and the next review. |
| Report | Deliverables and review inbox | Report, methodology, and dataset state; buddy/expert/journalist reviews; data checks and lineage. |
| Story + Some | Payload-linked deliverable detail | Story draft/changed/published state; Editor, expert, data-scientist, and publisher approvals; related social outputs. |
| External Distribution | Publication/distribution evidence | Canonical URLs, partners/outlets, publication facts, derived reach where available, and unresolved follow-up work. |

The first three stages live on the standing intake project's task; passing the
Editorial Evaluation gate promotes that task into an investigation project.
The remaining stages live on the project and its milestones, work,
deliverables, and approvals. The stage rail should be a **query over those
facts**, not a second editable project-status field. Engineering projects use a
different derived rail—design, build, review, merge, dev, prod, release—over the
same shell. **Synthesis.**

## Personas and jobs to support

| Persona | Main question | Best default surface | Important interaction |
| --- | --- | --- | --- |
| Journalist / editor | “What must happen before this story can be reviewed or published?” | Investigation project overview | Plain-language milestone and deliverable checklist; request/record approval; see a concise explanation of what is blocked. |
| Data scientist | “Which data, method, review, and publication steps remain?” | Project worklist and deliverables | Link a dataset/report deliverable to work; see provenance and verification state without navigating Git infrastructure. |
| Engineer / AI engineer | “What can I safely pick up, and what happened after I did?” | My work / ready frontier | Claim/start or release a task; open its worktree, brief, PR, checks, deploy trail, and agent activity. |
| DevOps / platform engineer | “Which change is unhealthy or has not reached production?” | Delivery and operations watch | Filter by environment/service; move from failing runtime/deploy fact to task, PR, and accountable owner. |
| Editor / reviewer | “What needs my judgment, and what evidence supports it?” | Review inbox | Open an approval-oriented detail view with changed content, artifacts, prior decisions, and a clear approve/request-changes action. |
| Producer / lead | “Which project is at risk and why?” | Portfolio | Scan project health, unresolved deliverables, overdue approvals, blockers, and stale/noisy facts before drilling in. |

**Synthesis.** A journalist should never have to learn branches, worktrees, or
Kubernetes to know whether a story can publish. Conversely, an engineer should
not need to update a separate editorial dashboard after a PR or deploy. The
same project needs different explanatory layers.

### One application, three levels of disclosure

Do not build separate “editorial Worklode” and “engineering Worklode” products.
Use the same objects and URLs with progressively deeper evidence:

1. **Outcome layer for everyone** — purpose, current stage, human owner, next
   decision, blockers, milestones, deliverables, and approvals in ordinary
   language.
2. **Work layer for practitioners** — tasks, dependencies, review threads,
   documents, datasets, branches, PRs, and the actions the current state permits.
3. **Evidence layer for specialists** — event provenance, claim/lease and agent
   sessions, CI detail, artifacts, deploy/runtime facts, lineage, drift, and
   correlation repair.

This is progressive disclosure, not permission by obscurity. Access control
still comes from Keycloak and the server; the UI merely stops making every
reader pay the cognitive cost of every subsystem. **Synthesis.**

### Automation is a per-item policy, not an assignee

The person creating a pitch, document, plan, or execution task should choose how
far AI may carry its downstream work. The control belongs on the object at
creation, inherits a project default, and is copied onto newly created children
so that a later project-default change cannot silently broaden existing
authority. The human owner remains visible at every level.

Do not expose the lifecycle as three unrelated automation checkboxes. Some
consequences are Worklode invariants; others are delegation choices:

| Event | Worklode consequence | Creator-controlled choice |
| --- | --- | --- |
| A spec is accepted by a human | Its planning need becomes visible. The current document/event model mints a `design` task asking how to decompose the spec into plans. | Leave the planning task queued, assign a person, or delegate it automatically to a planning agent. “Accepted but intentionally never plan” should be an explicit lifecycle decision, not a silent missing task. |
| A planning task becomes ready | Nothing starts merely because the task exists. | Manual delegation, suggested agent, or automatic delegation to an allowed planning agent. |
| A planning agent produces a plan | The plan remains a draft and requests review. The agent does **not** accept it. | Notify selected reviewers and optionally prepare a summary/diff; wait at the human acceptance gate. |
| A plan is accepted by a human | Acceptance atomically mints the execution tasks declared by that plan. This is the plan/task-set invariant, not optional automation. | Leave tasks in the ready queue or automatically delegate eligible tasks as dependencies clear. |
| An execution task becomes ready | The ranking and lease rules determine whether it is claimable; they do not require an agent to claim it. | Manual, suggested, or automatic execution, bounded by project, task kind, repository, budget, concurrency, environment, and time window. |

Offer four understandable presets, with an expandable policy preview:

1. **Manual** — create the required downstream work, but start no agents.
2. **Planning assist** — delegate planning automatically, then stop for plan
   review and acceptance.
3. **Execute accepted plans** — also dispatch ready tasks from plans a human has
   accepted.
4. **Bounded autopilot** — keep dispatching and use fixer/escalation paths within
   explicit scope, budget, concurrency, and expiry limits; still stop at human
   acceptance and approval gates.

The UI should say what will happen in verbs—“When this spec is accepted, create
one planning task and delegate it to the planning pool”—and preview the effective
policy before saving or starting a run. Each automatic action records the policy
version and actor that authorized it. A project-level **Pause automation** stops
new dispatch without rewriting task state or pretending already-running work
never happened. **Synthesis.**

## Recommended information architecture

Use a compact global shell with six primary destinations. Names are deliberately
plain; domain language should be tested with Sunstone users before committing.

1. **Home** — personalized “today”: work assigned to me, agent work I supervise,
   approvals awaiting me, mentions/notifications, and items blocked on my
   decision. This is an action queue, not an activity firehose.
2. **Projects** — portfolio list across engineering and investigations. Default
   rows show health, current milestone, next decision, project lead/crew,
   deliverable progress, and the one highest-signal risk.
3. **Project** — the shared project home. Its overview is a narrative status
   header followed by milestones, deliverables, work, approvals, and activity;
   tabs/lenses let experts reach execution and delivery evidence.
4. **Work** — a saved-view library for ready frontier, my work, team work,
   blocked, review, and inbox. List is the default; a board is an optional
   execution lens, never the canonical planner.
5. **Deliveries** — cross-project evidence for PRs, CI, environments, runtime
   warnings, CMS publications, datasets, and artifacts. It is an operational
   watch surface, not a second task tracker.
6. **Knowledge** — documents, decisions, research, and an explicit “needs
   planning / needs execution / stale / patched” view. It reflects the document
   lifecycle and coverage queries rather than copying a wiki.

Global search should accept an ID, natural language title, person, project,
deliverable address, repository, branch, PR number, or document reference. It
should return the object and its type, then retain filters as visible removable
chips. This applies Tana's type-aware retrieval pattern without requiring users
to understand tags. **Synthesis.**

### Core screen contract

| Screen | It must answer | Primary action |
| --- | --- | --- |
| Home | What needs my work or judgment today? | Open the highest-signal assignment, approval, or blocked decision; snooze a notification without hiding the work. |
| Intake | Which ideas need selection or an Editorial Evaluation decision? | Capture, refine, promote to a project, or close with rationale. |
| Portfolio | Which bounded investigations and standing engineering projects need attention? | Open the risk, overdue decision, or next milestone—not a generic dashboard tile. |
| Project | What is this project trying to ship, where is it in its real process, and what is stopping it? | Act on the next decision or enter the relevant work/deliverable. |
| Work detail | Who owns the outcome, is an agent working on it, what is blocked, and what happened after implementation? | Take the next legal human action, delegate/inspect agent work, or open the source tool. |
| Review / approval | What changed, what evidence supports it, and why am I an approver? | Approve, request changes, or reject with a recorded reason. |
| Delivery watch | What failed, stalled, or has not reached the intended environment/publication target? | Traverse the fact back to its project, task, owner, PR, artifact, or source system. |
| Knowledge | Which designs are stale, patched, unplanned, unexecuted, or accumulating notes? | Review the affected section or open the task that resolves it. |

### Project screen

The project screen should answer status before exposing machinery:

```
Project header: title · purpose · health · project lead · next decision
Progress strip: the seven Sunstone stages, or an engineering delivery rail
Milestone cards: each shows its outcome, open work, deliverables, blockers
Deliverables: Report | Dataset | CMS story | Service release, with reported state
Tabs: Overview | Work | Deliveries | Documents | Approvals | Activity
```

For a research project, “Publication” is meaningful; for an engineering project,
the same slot can show “Production” and environments. The UI must explain the
state origin, e.g. “Published by Payload at 14:32” or “Production deployment
awaiting Flux event,” rather than presenting a user-editable green checkbox.
This follows the reported-not-asserted deliverable rule. **Synthesis.**

### Data-science deliverables and review contract

“Data science done” must not collapse into a completed analysis task. The project
cockpit should declare a small deliverable set and show how every output is
verified, which revision was reviewed, and which judgment is still missing:

| Deliverable | Required evidence | Review and authoritative surface |
| --- | --- | --- |
| Dataset / data product | Stable address or Worklode label; source and transformation lineage; schema; version/snapshot; freshness and quality checks; access classification. | Science Lead or named data reviewer approves the declared dataset and checks in Worklode; the data platform remains authoritative for materialization and lineage. |
| Reproducible analysis | Repository and exact commit; environment/lock information; deterministic entry point; test and pipeline results; links from outputs back to the code revision. | Code review remains in GitHub. Worklode ingests the PR reviewer, decision, checks, and commit and presents them as one code-review gate. |
| Methodology | Versioned method document linked to the exact dataset and analysis revisions; source selection, cleaning, exclusions, transformations, assumptions, uncertainty, limitations, and known failure modes. | Science Lead and/or domain expert reviews the methodology bundle in Worklode, with the working document allowed to remain in Google Docs. Approval is against a recorded revision, not the floating link alone. |
| Report | Versioned report or PDF; claims/findings linked to the methodology and supporting outputs; limitations and publication readiness. | Buddy, domain expert, and journalist review the report as required by the Sunstone Way. Worklode owns the review request, decision, and evidence snapshot; the authoring tool owns prose editing. |
| Story and publication outputs | Payload entry, canonical URL, and Payload-reported draft/changed/published facts; links to the report findings used. | Editor, expert, data scientist, and publisher approvals as configured; Payload remains the writing and publishing surface. |

The project page should render these as a **review matrix**, not one progress
percentage. Columns are deliverable, current revision, automated checks, required
reviewers, review state, blocking reason, and source link. A review decision
records the evidence revision it covered; materially changing the linked code,
dataset, methodology, or report makes the old decision visibly stale and requests
a new review according to policy.

`Request changes` should create or unblock concrete correction work and preserve
the review thread. It must not turn a deliverable into an editable task or let a
reviewer manually assert that an artifact exists. **Synthesis.**

### Object detail: one stable side panel/full page

Use a responsive detail surface: a right-side panel on desktop where the list
remains usable, a full page on narrow screens, and a copyable canonical URL in
both. This mirrors the optional side-panel/center dialog choice in Jira and the
side-peek/full-page pattern in Notion. [Jira](https://support.atlassian.com/jira-software-cloud/docs/customize-your-view-of-the-board-and-backlog/),
[Notion](https://www.notion.com/help/views-filters-and-sorts)

Each task detail should contain, in this order:

- plain-language outcome and current status, owner/assignee, due expectation,
  blockers, and next permissible action;
- relevant context: body, images/attachments, linked design sections, parent
  milestone, linked deliverables, and related work;
- an **Execution** section for agents/engineers: lease holder, worktree, agent
  session, branch, PR, checks, deployment, and direct links to GitHub;
- an **Evidence** section that labels every fact as declared, observed, or
  reported, with timestamp and source;
- a chronological activity/provenance log, collapsed by default but always
  inspectable.

Actions must be permission- and state-aware. For example, a person can start
human work or assign ownership; an agent workflow can claim a ready task; a
reviewer can approve/request changes; neither can declare a production deploy.
The server remains the validator. **Synthesis.**

### Views, boards, and timelines

- Saved views are named queries, owned and scoped like Linear's durable views;
  personal display choices such as compact density are local, like Jira's.
  [Linear](https://linear.app/docs/custom-views),
  [Jira](https://support.atlassian.com/jira-software-cloud/docs/customize-your-view-of-the-board-and-backlog/)
- **List/table** is the high-information default for work, review, intake, and
  operations. It supports bulk triage and clear column labels.
- **Board** serves only active, human-understandable workflow states. Dragging
  is allowed only when it maps to a legal server transition; failed moves must
  say why and offer the next legal route. GitHub's direct manipulation is a
  useful expectation, but not authority. [GitHub](https://docs.github.com/en/issues/planning-and-tracking-with-projects/customizing-views-in-your-project/changing-the-layout-of-a-view)
- **Timeline** is for milestones, deliverables, project dependencies, and
  release/publication windows—not individual agent tasks. This takes Linear's
  project-level timeline boundary seriously. [Linear](https://linear.app/docs/timeline)
- **Graph/drift** is an expert, optional lens. Start with a list of exceptions
  and “why this matters”; use topology only to explore a selected relationship.

### AI in the human GUI

AI should appear as a participant and accelerator, not as the application's
organizing metaphor.

- Keep **human owner** and **agent delegate / active lease** visibly separate.
  “Liv owns this; Codex is working on it” is accurate. Replacing Liv's avatar
  with a bot makes responsibility ambiguous. This directly matches Linear's
  agent-delegation model while preserving Worklode's stronger lease semantics.
  [Linear assignment and delegation](https://linear.app/docs/assigning-issues)
- Put AI actions in context: summarize changes since my last visit, explain a
  blocker in plain language, draft a project update, suggest decomposition, or
  delegate this task. Do not make a blank chat box the home page.
- Show observable progress and outputs—task state, timestamps, commits, PR,
  tests, notes, escalation, and cost—rather than private reasoning or a raw
  transcript stream. The event log is the trustworthy explanation surface.
- Label generated summaries with their generation time and source objects, and
  keep a visible path to the underlying facts. A summary can become stale; an
  ingested publication or deployment fact does not become true because the AI
  said so.
- Preserve human gates. AI may prepare a review or recommendation, but document
  acceptance, Editorial Evaluation, and role-required approvals remain explicit
  human decisions.

**Synthesis.** The highest-value AI UI is “explain and act on this governed
object,” backed by Worklode context. A general organization chatbot can come
later, after the project, decision, and evidence surfaces are trustworthy.

### The overnight agent supervisor

“Herd agents while I sleep” needs a bounded run surface, not a live transcript
wall. Before starting a run, show one reviewable contract:

- project(s) and repositories in scope;
- effective automation preset and allowed planning/execution agent pools;
- eligible task kinds and ready-set filters;
- concurrency, token/spend budget, start/end time, and stop conditions;
- allowed environments and actions, with production and human approvals still
  gated;
- secret-readiness preflight, showing names/readiness only; and
- the person accountable for the run and where escalations will wait.

During the run, organize work into **ready, running, waiting on another task,
waiting on a human, failed, and completed**. Each running row shows owner, agent,
lease age, last durable event, cost, PR/check state, and the next expected signal.
The primary controls are Pause new work, Stop after current tasks, and open the
specific exception. Claims and leases remain the collision-control mechanism.

Agents may invoke a higher-tier fixer for a bounded planning defect, as spec 028
defines. Ambiguous judgment, a substantive document change, an undeclared secret,
budget exhaustion, repeated failure, or a required human review becomes a visible
escalation and stops only the affected frontier unless policy says to stop the
whole run. The morning view summarizes what shipped, what changed, money/tokens
spent, tasks retried, outstanding decisions, and the shortest path to resume.
**Synthesis.**

## What to borrow and what to avoid

Borrow: one-record/many-views; rich detail context; a separation of intake and
active work; saved cross-project queries; direct manipulation where it maps to
a real transition; and a project-level timeline. These patterns lower cognitive
load without hiding the relationship between planning and execution.

Avoid: sprint/cycle as the universal organizing unit; a configurable workflow
per team; dashboards composed from arbitrary metrics; a board as the only way
to understand status; a generic “Done” that conflates merge, deploy, and
publication; user-maintained deployment facts; and surfacing agent internals
as mandatory editorial vocabulary. **Synthesis.**

The sharpest product distinction is the **evidence line**: every status shown
by the GUI should identify whether it is a human decision, a system-observed
fact, or an unverified/missing correlation. That allows a nontechnical user to
trust the interface without asking them to inspect CI logs, and lets operators
see when a green status is merely stale. **Synthesis.**

## First interactive MVP: three vertical slices

The first interactive version should prove the cockpit's three distinctive
jobs together. A generic read-only project page followed months later by the
real workflows would not test the product thesis.

### Slice 1 — intake / pitch / idea to project

Provide fast pitch capture into the standing intake project, a guided litmus-test
view, comments/refinement, and the Editorial Evaluation queue. The Gate 3 action
records the journalist/data-scientist recommendation and Editor/Science Lead
decision, then either closes the idea with rationale or atomically promotes it
to a project with a crew (stored as participants), default milestones, declared deliverables,
approval requirements, `seeded_by`, and an automation policy. The new project
opens directly in its cockpit with the next decision highlighted.

### Slice 2 — data-science deliverables and three review lanes

Implement the review matrix for dataset, reproducible analysis, methodology,
and report. Prove three distinct lanes end to end:

1. GitHub code review and CI evidence are ingested and summarized without
   replacing GitHub.
2. A methodology revision can be submitted, reviewed by the required role,
   changed, and re-requested with its dataset/code evidence attached.
3. A report revision can receive buddy/expert/journalist decisions and cannot
   appear review-complete when its supporting methodology revision changed.

Approval/request-changes, comments, assignment, and evidence snapshots need API
and CLI surfaces as well as the web UI so agents and humans participate in the
same review system.

### Slice 3 — bounded unattended execution

Add the per-item automation presets and one overnight-run screen. A user can
scope a run to selected projects/repositories, set budget/concurrency/expiry,
preflight secrets and human gates, then start or pause dispatch. The UI groups
ready/running/waiting/failed/completed work and produces a morning handoff.
Planning agents may draft plans, but wait for human acceptance; execution agents
may continuously claim tasks from already accepted plans as dependencies clear.

The acceptance demonstration is one traceable journey: capture a pitch, promote
it, declare its research outputs, review code/methodology/report, accept a plan,
run eligible execution unattended, and return to a sourced morning summary with
the remaining human decisions obvious.

Defer configurable dashboards, arbitrary workflows, sprint/cycle machinery,
full graph exploration, embedded GitHub/Kubernetes/CMS clones, and a general
organization chatbot. They do not prove these three jobs.

## Accessibility, cross-device, and nontechnical implications

- Build to [WCAG 2.2](https://www.w3.org/TR/WCAG22/) from the first component:
  keyboard operation, semantic headings/landmarks, labelled controls,
  sufficient contrast, and announcement of async state/action results. Provide
  a visible focus indicator ([WCAG 2.4.7](https://www.w3.org/WAI/WCAG22/Understanding/focus-visible)),
  do not use colour as the only status signal
  ([WCAG 1.4.1](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html)),
  and make pointer targets at least 24 by 24 CSS pixels or provide the required
  spacing/equivalent control
  ([WCAG 2.5.8](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum)).
  Accessibility is not a cosmetic polish phase.
- Keep primary creation and approval flows possible without drag-and-drop;
  every board/timeline operation needs an equivalent menu/form action.
- Design desktop-first for dense engineering/operations evidence, but make
  Home, project overview, approvals, task read/submit, and notification
  actions work on a Chromebook and mobile-width browser. Narrow view means
  full-page detail, fewer columns, and progressive disclosure—not a squeezed
  desktop table.
- Use ordinary verbs in primary UI: “Request review,” “Needs changes,”
  “Published,” “Waiting on data.” Show `in_review`, Flux, worktree, and other
  system terms as secondary evidence with short explanations.
- Preserve copyable URLs and browser back/forward behaviour for every object
  and saved view; this is essential for Google Workspace, Google Chat/email,
  and editorial collaboration.

## Integration surfaces

| Surface | GUI role | Rule |
| --- | --- | --- |
| Keycloak | sign-in, actor identity, group-gated approvals | Use the existing Keycloak-primary direction; never make GitHub identity the sole editorial identity ([spec 023](../specs/023-keycloak-primary-auth.md)). |
| GitHub | PR/commit/review/check links and webhook-derived facts | Show a deep link plus normalized evidence; GitHub remains the code-review UI where appropriate. |
| AI coding harnesses | agent sessions, claim/worktree status, escalation | Expose a supervisory summary and links; retain CLI/harness as the execution control plane ([spec 024](../specs/024-multi-harness-integration.md)). |
| Kubernetes / Flux / Hetzner | delivery, environment, runtime signals | Present service/environment state as observed evidence; clicking through can open the task/PR/event chain. |
| Payload CMS | story publication and editorial deliverables | Emit idempotent publication state; show status and canonical story URL, not a duplicated CMS editor. |
| Data platform / pipelines | dataset and report verification | Report artifacts/datasets through the same deliverable evidence model so research and engineering share one project status. |
| Google Workspace | links to source docs, meeting context, notifications | Treat Drive/Docs as linked working material and Google Chat as the first lifecycle notification channel; do not pretend Worklode replaces collaborative writing. |
| 1Password | task secret readiness, operator guidance | Show only symbolic requirements and safe readiness/materialization state. Never show values or `op://` references; 1Password remains the decryption authority ([spec 017](../specs/017-task-secrets.md)). |
| Fleet | exceptional device-readiness guidance for operators/admins | Keep MDM out of ordinary project detail. Surface a device-posture blocker only when it actually prevents an authorized action, then deep-link to the owning admin flow. |

## Decision summary

**Synthesis.** Build Worklode as an evidence-backed project home with several
purposeful lenses, not as a replacement GitHub board or a configurable generic
workspace. The default experience should be “what is this project trying to
ship, what is stopping it, who needs to decide, and what evidence says so?”
The advanced experience should let engineers and agent supervisors traverse the
same answer down to worktrees, PRs, CI, deployments, runtime events, and
provenance—without making that complexity the price of entry for journalists
and editors. The first product proof is the complete intake-to-project path, a
trustworthy data-science review matrix, and bounded unattended execution whose
morning handoff is more useful than watching the agents live.
