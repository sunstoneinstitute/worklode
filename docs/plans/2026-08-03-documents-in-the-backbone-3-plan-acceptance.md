---
status: draft
implements:
  - docs/specs/025-documents-in-the-backbone.md#sec-4
  - docs/specs/025-documents-in-the-backbone.md#sec-5
  - docs/specs/025-documents-in-the-backbone.md#sec-7
  - docs/specs/025-documents-in-the-backbone.md#sec-10
requires:
  - 2026-08-03-documents-in-the-backbone-2-document-store.md
---
# Documents in the backbone 3/4: plan acceptance mints the tasks

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of 4 (6 tasks; numbering restarts at 1 per part). See
part 1 for the series map. Part 2 must be merged first.

**Goal:** Implement 025 §4, §5, §7 and §10: accepting a plan document mints
its execution tasks from the plan task format — draft rows carrying
`plan_doc`, kind/priority/skills/blocking read from each task's metadata
block, nothing minted above them — plan-to-plan `blocks` edges gate the
ready set, and the `lode doc` verbs land, store-backed.

**Architecture:** `lode doc accept` on a plan runs one `RecordEvent`
transaction: parse the plan body's task definitions, `CreateTask` each as a
draft with `plan_doc` set and its declared kind, priority and skills, wire
the intra-plan `blockedBy` numbers as `blocks` edges between the minted
tasks, flip the doc to `accepted`. The invariant `doc.status = accepted ⟺
its tasks exist` holds by construction because part 2 rejected plan
acceptance until this transaction existed, and no other path writes
`plan_doc`. The ready set treats a task as blocked while any task in a plan
that `blocks` its plan is open — the same predicate spec 005 already runs,
evaluated over a set (025 §7) — expressed as one SQL condition shared by
`readyCandidates` and `IsBlocked`.

**Tech Stack:** Go 1.25+, Postgres, cobra CLI.

**Spec:** `docs/specs/025-documents-in-the-backbone.md` §4, §5, §7, §10

**The plan task format** (canonical definition lands in spec 025 and
`docs/authoring-design-docs.md`; this is what Task 1 parses):

````markdown
## Tasks

### Task 1 — Short imperative title

```yaml
kind: feature            # feature | bug | chore | design
priority: medium         # critical | high | medium | low
skills:                  # skills the executing agent loads before starting
  - superpowers:test-driven-development
blockedBy: [ ]           # task numbers within this plan
```

Prose: what to do, which files to touch, the test that proves it.

- [ ] step
````

`N` enumerates from 1 within each plan file; `blockedBy` holds task numbers
within the same file only and becomes `blocks` edges between the minted
tasks; `skills` names real `plugin:skill` ids and lands in the existing
`tasks.skills` pinned-skills column.

**Read first:**
- 025 §5 (the two-acts table and the nullable `plan_doc`), §7 (the query
  table), §10 (the verb set), §13 AC2/AC4
- `internal/store/docs.go` (part 2 — `AcceptDoc`'s plan stub is what Task 2
  here replaces)
- `internal/store/tasks.go:36-46` (`TaskInput` — `Skills` already exists;
  minted skills ride it into the `tasks.skills` jsonb column from migration
  0007, surfaced in every brief), `:675-717` (`blockedCondition`,
  `IsBlocked`); `internal/store/ranking.go` (`readyCandidates`)
- `docs/specs/026-design-doc-queries.md` §2 — the selector semantics
  `--needs-planning`/`--needs-execution` must match; 026 AC9 promises that a
  store-backed loader is the whole migration, and this part is that loader

**Conventions:** as part 1.

**Interaction with spec 026:** 026 implements `lode doc list/show/sections`
against the *git mirror*; no plan for that read surface has been executed. If
its commands exist when this part runs, Tasks 4–5 swap their data source to
the store (026 AC9) rather than adding parallel verbs; if they do not, Tasks
4–5 create the commands store-backed and 026's mirror-backed variant is
moot. Either way the verb names, flags and semantics are 025 §10's.

**Non-goals:** `lode doc coverage` (needs the `.worklode/implements.yaml`
deriver, never built — stays with the 014 plan's deferred table);
`doc sections`/`--resolved` consolidation (026's rendering work, orthogonal
to the store); review-task ceremony and crit wiring (025 §12); the lode
plugin skills for guided flows (live in the claude-plugins repo, not here);
Milestone (v2, 025 §12); tier-2 shorthand resolution for foreign corpora
(reads these `docs` rows, but belongs to 026 §4.2's owner).

---

## Decisions the spec leaves open, taken here

- **A `blocks` edge from an unminted plan blocks.** §7's predicate reads "any
  task in a blocking plan's set is open"; an accepted-but-unexecuted or still
  draft blocking plan has no closed set, and §10's `--needs-execution` calls
  an unminted set unfinished, so the edge blocks until the blocking plan's
  tasks all close. Stated here because the literal §7 sentence would read an
  empty set as unblocked.

## File structure

| File | Responsibility |
|---|---|
| `internal/designdoc/plantasks.go` (+ test) (new) | plan-task-format parsing |
| `internal/store/docs.go` | the plan branch of `AcceptDoc`; `NeedsPlanning`/`NeedsExecution` queries |
| `internal/store/tasks.go`, `ranking.go` | `planBlockedCondition`; `TaskInput.PlanDoc`; filters |
| `internal/store/metrics.go` | `worklode_doc_plan_tasks_minted_total` |
| `internal/api/docs.go`, `tasks.go` | accept response gains the minted set; `plan_doc` on task JSON; list filters |
| `internal/cli/client.go` (new methods), `internal/cmd/doc.go` (new) | the `lode doc` verbs |
| `e2e/docs_test.go` | plan accept → claim flow |

---

## Tasks

### Task 1 — Parse the plan task format in `designdoc.PlanTasks`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/designdoc/plantasks.go`, `internal/designdoc/plantasks_test.go`

- [ ] **Step 1: Write the failing tests**

Table-driven over plan bodies in the format above:

| Body | Expectation |
|---|---|
| a `## Tasks` section holding three `### Task N — Title` sections, each opening with a `yaml` fence | three defs in source order; titles are the text after the em dash; each def carries the fence's kind, priority, skills, blockedBy; the prose after the fence is the body |
| a task section with **no** yaml fence, or a fence missing keys | defaults: `kind: feature`, `priority: medium`, no skills, no blockers |
| `blockedBy: [1]` on Task 2 | `BlockedBy = []int{1}` |
| `blockedBy` naming a task number not in the file, or a task's own number | error naming the task and the number |
| unknown `kind` or `priority` in the fence | error naming the task and the value (validated against `ns.TaskKinds` and the priority set) |
| task headings outside a `## Tasks` section | ignored — only the `## Tasks` section enumerates |
| a plan with a `## Tasks` section but no task headings, or no `## Tasks` section | error: "plan defines no tasks" — accepting it would mint nothing and break AC2's ⟺ |
| duplicate task numbers | error — `blockedBy` references would be ambiguous |

- [ ] **Step 2: Implement**

```go
// PlanTask is one task definition in a plan document's ## Tasks section —
// the plan task format 025 §5 mints from (docs/authoring-design-docs.md
// carries the canonical definition).
type PlanTask struct {
	Number    int
	Title     string   // heading text after "Task N — "
	Body      string   // the section's own content, yaml fence excluded
	Kind      string   // default "feature"
	Priority  string   // default "medium"
	Skills    []string // plugin:skill ids the executing agent loads
	BlockedBy []int    // task numbers within this plan
}

var planTaskHeadingRE = regexp.MustCompile(`^Task\s+(\d+)\s+—\s+(.+)$`)

// PlanTasks extracts the task definitions the accept transaction mints:
// the `### Task N — Title` sections under the `## Tasks` heading, each
// optionally opening with a yaml metadata fence (kind, priority, skills,
// blockedBy). Validation errors name the task; the numbers label tasks for
// blockedBy and must be unique, but need not be contiguous.
func PlanTasks(d *Document) ([]PlanTask, error)
```

Parsing notes: the metadata fence is the first fenced block of the section
*only if* it starts the section body (blank lines aside) and its info string
is `yaml` — decode with `yaml.v3` + `KnownFields(true)` so a typoed key is
an error, matching `parseFrontmatter`'s stance. The section body minus the
fence becomes `Body`. Heading matching accepts `-`/`–` as well as `—` (em
dash), normalising rather than rejecting on dash width.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/designdoc/ -run TestPlanTasks -v
```

---

### Task 2 — Mint the tasks in the accept transaction

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/store/docs.go` (replace the plan stub in `AcceptDoc`),
  `internal/store/tasks.go` (`TaskInput.PlanDoc`), `internal/store/metrics.go`
- Test: `internal/store/docs_test.go`

- [ ] **Step 1: Write the failing tests**

- Accepting a draft plan with three task definitions creates exactly three
  draft tasks in the plan's project, each with `plan_doc` = the doc id,
  title, body, kind, priority and skills from its definition (skills land in
  `tasks.skills` — assert they surface exactly as `task add --skill` pins
  do), created_by the accepting actor — and **no row above them**: assert no
  task references the plan except via `plan_doc`, and no `child_of` edge was
  written.
- `blockedBy: [1]` on Task 2's definition yields a `blocks` edge from minted
  task 1 to minted task 2; the blocked task is absent from the ready set
  until task 1 closes (the existing `blockedCondition`, no new machinery).
- The invariant both ways (AC2): before accept, zero tasks carry the doc's
  id; after, the count equals the definition count; a second accept is
  `ErrInvalidInput` (already accepted), so the set can never double-mint.
- A plan whose body fails `PlanTasks` (no tasks, dangling `blockedBy`, bad
  kind) refuses to accept with the parser's error; status stays `draft`.
- Assignee gating applies to plans exactly as to specs.
- `worklode_doc_plan_tasks_minted_total` increments by the minted count.

- [ ] **Step 2: Implement**

- `TaskInput` gains `PlanDoc int64` (0 = none); `CreateTask` writes the
  column when set. No other writer of `plan_doc` exists anywhere. Skills
  need nothing new: `TaskInput.Skills` → `tasks.skills` (migration 0007)
  already carries pinned skills into every brief, which is exactly what
  "skills the executing agent loads before starting" means.
- `AcceptDoc`'s plan branch: `designdoc.Parse` the stored body →
  `designdoc.PlanTasks` → first pass loops
  `CreateTask(tx, now, TaskInput{ProjectID: doc.ProjectID, Title, Body,
  Priority, Kind, Skills, CreatedBy: actorID, Draft: true, PlanDoc: doc.ID})`
  recording number → id; second pass wires `AddEdge(tx, now, id[m], id[n],
  "blocks")` for each task n with m ∈ BlockedBy — then the status flip. All
  on the caller's `tx`, so the API's single `RecordEvent` (`doc.accepted`)
  is the one transaction 025 §5 requires.
- Metrics: `docTasksMinted prometheus.Counter`
  (`worklode_doc_plan_tasks_minted_total`), nil-safe, added in
  `newStoreMetrics`.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store/ -run 'TestDocAccept|TestPlanMint' -count=1 -v
```

---

### Task 3 — Surface `plan_doc` on tasks

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/store/tasks.go` (`Task.PlanDoc`, `taskColumns`, `scanTask`,
  `TaskFilter.PlanDoc`), `internal/api/tasks.go` (`taskJSON`, list param
  `plan_doc`), `internal/api/docs.go` (accept response), `internal/cli/client.go`,
  `internal/cmd/task.go` (`task list --plan`)
- Test: `internal/api/tasks_test.go`, `internal/api/docs_test.go`

- [ ] **Step 1: Write the failing tests**

- `GET /tasks/{id}` on a minted task shows `"plan_doc": <id>`; a task no plan
  authored shows none (omitempty — its absence is the correct answer, 025 §5).
- `GET /tasks?plan_doc=<id>` returns exactly the plan's set — the query that
  *is* the plan's task set (§1).
- `POST /docs/{id}/accept` on a plan returns the doc **and** the minted tasks
  in one response, so the CLI can print what one act created.
- `lode task list --plan <ref>` renders that set.

- [ ] **Step 2: Implement**

Append `plan_doc` to `taskColumns` (last, matching the skills precedent at
`tasks.go:315-320` — comma-free entry, positional scans elsewhere
unaffected). `Task.PlanDoc int64`; filter wiring mirrors `Kind`. CLI `--plan`
resolves the doc ref via the part-2 list endpoint, then filters.

- [ ] **Step 3: Verify and commit**

---

### Task 4 — Gate the ready set on plan-to-plan `blocks`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/store/tasks.go` (`planBlockedCondition`, `IsBlocked`),
  `internal/store/ranking.go` (`readyCandidates`), `internal/store/docs.go`
  (edge write already handles `blocks`; add validation: both ends plans)
- Test: `internal/store/ranking_test.go`, `internal/store/docs_test.go`

- [ ] **Step 1: Write the failing tests**

Fixture: plan A and plan B accepted (each minting tasks), edge A `blocks` B.

- B's ready tasks are absent from `readyCandidates` and rejected by `Claim`
  (`ErrBlocked`) while any A task is open.
- Closing every A task (walk them to `merged`/`abandoned`) releases B's set —
  no edge removal, no event needed: the predicate is live.
- A draft blocking plan (unminted set) blocks — see the decisions section.
- Tasks with `plan_doc IS NULL` are never affected.
- `doc_edges` rejects a `blocks` edge whose ends are not both plan docs
  (`ErrInvalidInput`).

- [ ] **Step 2: Implement**

In `tasks.go`, beside `blockedCondition`:

```go
// planBlockedCondition holds a task while its plan is ordered after another
// plan whose work is not done (025 §7): a blocks edge between the two plan
// documents, evaluated over the blocking plan's task set — open tasks, or a
// set not yet minted (the blocking doc still draft).
const planBlockedCondition = `t.plan_doc IS NOT NULL AND EXISTS (
    SELECT 1 FROM doc_edges de
    WHERE de.type = 'blocks' AND de.to_doc = t.plan_doc
      AND (EXISTS (SELECT 1 FROM tasks bt
                   WHERE bt.plan_doc = de.from_doc
                     AND bt.state NOT IN ` + closedStates + `)
           OR EXISTS (SELECT 1 FROM docs bd
                      WHERE bd.id = de.from_doc AND bd.status = 'draft')))`
```

`readyCandidates` adds `AND NOT (` + the condition + `)`; `IsBlocked` ORs it
in (aliasing the task row as `t` via a one-row subquery), so the claim path
and the ready set cannot disagree. No new metrics: no new op, and claim
outcomes are already counted.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store/ -run 'TestPlanBlock|TestReady|TestClaim' -count=1
```

---

### Task 5 — Ship the `lode doc` verbs

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4]
```

**Files:**
- Create: `internal/cmd/doc.go`, `internal/cmd/doc_test.go`
- Modify: `internal/cli/client.go` (doc methods), `internal/store/docs.go`
  (`NeedsPlanning`, `NeedsExecution`), `internal/api/docs.go` (list selectors)
- Test: `internal/api/docs_test.go`

- [ ] **Step 1: Write the failing tests**

Selector semantics are 026 §2's, store-backed:

- `NeedsPlanning`: accepted specs having ≥1 section that no accepted plan's
  `implements` edge names — a whole-document edge (`to_anchor IS NULL`)
  covers every section the doc has, present and future; overlap between
  plans is legal and unremarked. Output: doc + unplanned anchors.
- `NeedsExecution`: accepted plans whose task set has any non-closed task.
  This deviates from 025 §10's "unminted or unfinished" deliberately:
  through the accept path an accepted-but-unminted plan cannot exist, and
  the only unminted accepted plans are part 4's imported *spent* plans,
  which must not be reported as pending work. The `blocks` predicate
  (Task 4) covers the ordering need §10's "unminted" arm served. Flagged
  to the spec owner alongside the import carve-out.
- CLI table tests: `lode doc new --kind spec|adr|plan --file <md>` (reads the
  file, POSTs, prints the id), `lode doc list [--kind --status
  --needs-planning --needs-execution]` (conflicting `--status` with
  `--needs-planning` is an error, not an empty result — 026 §2.1),
  `lode doc show <ref>` (raw body; ref = id, number, or slug),
  `lode doc accept <ref>` (prints the doc and, for a plan, the minted task
  ids), `lode doc revise <ref>` / `lode doc revise <ref> --file <md>` /
  `lode doc revise <ref> --accept`, and `lode doc anchors <file>` — the
  author's local pre-accept lint (§10, from 014 §10): parse the file and
  report duplicate anchors, anchor/number disagreement, depth over
  `designdoc.DepthLimit`, and (for a plan) `designdoc.PlanTasks` errors, no
  server involved.

- [ ] **Step 2: Implement**

`NeedsPlanning`/`NeedsExecution` are single SQL queries in `docs.go` (no Go
set arithmetic — the sets live in the database now). API: `GET
/api/v1/docs?needs_planning=true|needs_execution=true`. CLI follows the
`internal/cmd/task.go` conventions: project scoping via `scope.go`, `--json`
via the root flag, acceptance is **never** implied by any other verb — one
verb, one deliberate act (025 §3).

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/cmd/ ./internal/api/ ./internal/store/ -count=1
```

---

### Task 6 — Prove plan acceptance end to end

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

**Files:**
- Modify: `e2e/docs_test.go`

- [ ] **Step 1: Write the test**

Extend the part-2 lifecycle test: author a plan doc in the plan task format —
two task definitions where Task 2 declares `blockedBy: [1]` and a `skills`
list — plus a doc-level `blocks` edge from an earlier plan; accept the
earlier plan and close its tasks through claim → done; accept the second
plan — assert its two tasks exist as drafts with `plan_doc`, the declared
kind/priority, the skills pinned, the intra-plan `blocks` edge wired, and
nothing else created; mark them ready; `claim --next` hands out task 1 only
(task 2 is edge-blocked) and only after the first plan's set closed;
`lode doc list --needs-execution` lists the plan until both tasks close,
then does not.

- [ ] **Step 2: Verify and commit**

```bash
go test -race -count=1 -tags e2e ./e2e/
```

---

## Done when (maps to 025 §13)

1. AC2: accept mints the tasks and their `plan_doc` references in one
   transaction — with the declared kind, priority and skills, and the
   intra-plan `blocks` edges — creates no row above them, and the ⟺
   invariant is tested in both directions.
2. AC4: two accepted plans under one spec have no row above either set; a
   plan-to-plan `blocks` edge orders them; `--needs-planning` and
   `--needs-execution` answer from queries alone.
3. AC5: `lode doc accept` is manual and assignee-gated end to end.
4. The plan-doc `- [ ]` checkbox convention is now replaceable: the tasks'
   state is the execution state (the files retire in part 4).
