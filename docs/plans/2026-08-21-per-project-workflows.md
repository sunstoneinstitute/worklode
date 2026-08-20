---
status: draft
covers:
- docs/specs/045-per-project-workflows.md#sec-1
- docs/specs/045-per-project-workflows.md#sec-2
- docs/specs/045-per-project-workflows.md#sec-3
- docs/specs/045-per-project-workflows.md#sec-4
- docs/specs/045-per-project-workflows.md#sec-5
- docs/specs/045-per-project-workflows.md#sec-6
- docs/specs/045-per-project-workflows.md#sec-8
- docs/specs/045-per-project-workflows.md#sec-9
blocks:
- 2026-08-21-workflow-rule-engine.md
---
# Per-project workflows — implementation plan

Implements spec 045: the static `legalTransitions` table becomes the built-in
`default` workflow, projects declare their own state machines in
`projects.workflows`, and every state-reading call site behaves per the
resolutions in 045 §5. The plan is ordered so the subsumption lands first
with zero behavior change (the equivalence test is the proof), and the
per-project variation switches on afterward.

Throughout: the wire shape for workflows lives in `internal/model` (ADR 036 —
it crosses the HTTP boundary), the rules and guard live in `internal/store`,
and no state name outside the nine-state vocabulary appears anywhere.

## Tasks

### Task 1 — Vocabulary, core edges, entry table, and the workflow type

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Pure Go, no schema change, no behavior change.

In `internal/model`: `Workflow` (`Description string`, `States []string`,
`Transitions [][2]string`) and `ProjectWorkflows` (`Default string`,
`Workflows map[string]Workflow`), stdlib-only, wire field names.

In `internal/store` (new `workflow.go`): the nine-state vocabulary as a
first-class constant slice; the mandatory core set; `coreEdge(from, to)`
implementing 045 §1.2 over the whole vocabulary; the §1.3 entry table;
`impliedEntries(states)`; `ValidateWorkflows(pw model.ProjectWorkflows)`
enforcing every §4.2 rule except the open-task reference check (Task 3) with
errors that name the violated rule; `builtinDefault()` returning the
all-nine-states workflow.

Tests: table tests for `coreEdge` and validation; **the equivalence test** —
core edges plus the built-in default's implied entries equal the current
`legalTransitions` map verbatim (import it while it still exists; Task 2
repoints the test at the old table's literal, kept as a test fixture).

- [ ] model types + `rule_test.go`/`deps_test.go` stay green
- [ ] store rules + validation with named-reason errors
- [ ] equivalence test pinning builtin default == legalTransitions

### Task 2 — Migration 0038 and the workflow-aware guard

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-migrations
blockedBy: [1]
```

Migration `0038_project_workflows`: `ALTER TABLE projects ADD COLUMN
workflows jsonb; ALTER TABLE tasks ADD COLUMN workflow text;` — down drops
both. No backfill; `tasks_state_check` untouched.

`Transition` resolves the governing workflow in-tx (task override → project
default → builtin, 045 §3) and applies 045 §2's legality rule; the
`ErrBadTransition` message names the governing workflow when the edge exists
in the vocabulary but not in the workflow. Delete `legalTransitions`;
`hierarchy_resolve.go:104-118`'s direct table reads use the core rules;
`containerForbiddenStates` becomes "every non-core state". `allStates()`
derives from the vocabulary constant, keeping
`TestTaskStateShapeMatchesStateMachine` honest.

Tests: minimal-workflow project refuses undeclared entries and allows every
core edge; stranded state keeps its core exits; hierarchy roll-up unchanged
under a custom workflow; full store suite green with the column NULL
everywhere (byte-for-byte old behavior).

- [ ] migration pair + kustomization listing
- [ ] Transition resolves and enforces per 045 §2
- [ ] legalTransitions deleted, hierarchy on core rules

### Task 3 — Store CRUD for workflows and the task override

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

`GetProjectWorkflows` (NULL → implied builtin) and `SetProjectWorkflows`
(validate via Task 1, plus the open-task reference check: removing/renaming
a workflow that open tasks name is refused listing them; `taskClosed` is the
openness predicate). `SetProjectWorkflows` runs through `RecordEvent` with
type `project.workflows_set`, full new object and actor in the payload.
Task-side: `EditTask` accepts `workflow`, validated against the project's
defined names; `CreateTask` accepts it too. The `done_state` compatibility
warning (045 §7) is returned, not enforced, from both `SetProjectWorkflows`
and `SetRepoDoneState`.

- [ ] Get/Set with validation, eventing, open-task refusal
- [ ] tasks.workflow settable at create and edit
- [ ] done_state mismatch surfaces as a warning string

### Task 4 — API endpoints, guard table, metrics

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`GET`/`PUT /api/v1/projects/{id}/workflows` in `internal/api`, wired through
`routeGuards` with a new `workflow.write` permission granted to `user` and
`admin` (GET rides `project.read`). PUT returns 422 with the store's named
validation reason. `worklode_workflow_writes_total{outcome}` in the owning
package's `metrics.go`, nil-safe, registered from `serve.go`, outcomes
`ok`/`invalid`/`error`. CLI client methods in `internal/cli`.

- [ ] routes + guards (NewServer boot check passes)
- [ ] metrics with tests
- [ ] cli client methods

### Task 5 — Watcher rule: review on workflow change

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

A `review-on-workflow-change` rule in `internal/watcher` (pure function,
same shape as doc-lifecycle): on `project.workflows_set`, mint a `review`
task "Review workflow change on <project>" naming the changed workflow
names, suppressed while an open review task from this rule exists for the
project (executor supplies the guard fact; a body marker line identifies
this rule's tasks). Executor wiring in `internal/api` alongside
`docwatch.go`; rule label joins the existing watcher metric.

- [ ] pure rule + table tests
- [ ] executor guard query + wiring under BackgroundCtx
- [ ] suppression proven by test

### Task 6 — CLI surface and help-text sweep

```yaml
kind: feature
priority: medium
skills: [ ]
blockedBy: [4]
```

`lode project workflow show [--json]`, `set --file` (`-` for stdin),
`validate --file` (client-side, same rules via a small exported store
helper); `lode task edit --workflow <name>` and `lode task new --workflow`.
Reword help texts that assert legality: `lode task done`'s stale
`(in_review -> merged)` short, `lode task list --status` wording unchanged
(vocabulary), `reopen` help stays (core). Check `docs/agent-surfaces.md` and
the plugin skills for hardcoded invocations touched by the new subcommands.

- [ ] new subcommands with --json paths
- [ ] help-text corrections
- [ ] agent-surfaces register updated

### Task 7 — Resolver and hooks resolve against the workflow

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`ResolveDelivery`: tail steps advance only along the governing workflow's
declared entries (045 §5.3); pre-merge jump and container early-return
unchanged; `done_state` semantics preserved. PR-opened handler
(`hooks/github.go:381`): attempt `in_progress → in_review`, tolerate
`ErrBadTransition` silently when the workflow lacks `in_review`. The
vocabulary-static prefilters (`TasksBelowFrontier`, `PollCandidates`,
`mergeCandidateStates`) stay as they are — assert in a comment that the
guard is authoritative.

Tests: prod-only workflow ignores dev frontier and lands `merged →
deployed_prod`; released workflow ignores prod frontier; PR-opened on a
no-review workflow leaves the task `in_progress` and fails nothing.

- [ ] resolver walks declared entries only
- [ ] PR-opened tolerance with test
- [ ] prefilter comments

### Task 8 — e2e: a custom-workflow project through the public surface

```yaml
kind: feature
priority: medium
skills: [ ]
blockedBy: [5, 6, 7]
```

One scenario in `e2e/` (public surfaces only): PUT a prod-from-main workflow
(no `deployed_dev`, no `in_review`), verify the review task was minted and
nothing blocked; open a PR — task stays `in_progress`; land and deploy prod
— task goes `merged` then `deployed_prod`; reopen works. A second assertion
pass runs the existing smoke flow on an untouched project to pin the
NULL-column equivalence.

- [ ] scenario green under `make test-e2e`
- [ ] untouched-project equivalence assertions
