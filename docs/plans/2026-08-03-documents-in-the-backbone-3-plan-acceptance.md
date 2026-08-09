---
status: accepted
covers:
  - docs/specs/025-documents-in-the-backbone.md#sec-4
  - docs/specs/025-documents-in-the-backbone.md#sec-4.1
  - docs/specs/025-documents-in-the-backbone.md#sec-5
  - docs/specs/025-documents-in-the-backbone.md#sec-7
  - docs/specs/025-documents-in-the-backbone.md#sec-10
requires:
  - 2026-08-03-documents-in-the-backbone-2-document-store.md
---
# Documents in the backbone 3/4: plan acceptance mints the tasks

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 3 of 4 (7 tasks; numbering restarts at 1 per part). See
part 1 for the series map. Part 2 must be merged first.

**Goal:** Implement 025 §4, §4.1, §5, §7 and §10: accepting a plan document
mints its execution tasks from the §4.1 `## Tasks` declarations — draft rows
carrying `plan_doc`, kind/priority/skills/blocking read from each task's
metadata block, nothing minted above them — plan-to-plan `blocks` edges gate
the ready set, the `lode doc` verbs land store-backed, and qualified skill
pins resolve with §4.1's after-colon fallback.

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

**Spec:** `docs/specs/025-documents-in-the-backbone.md` §4, §4.1, §5, §7, §10

**The plan task format** (canonical definition: 025 §4.1, mirrored in
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

`N` enumerates from 1 in document order without gaps, within each plan file;
`blockedBy` holds task numbers within the same file only and becomes
`blocks` edges between the minted tasks; `kind` takes only the four kinds a
plan may mint (never `review`/`spike`, §4.1); `skills` names `plugin:skill`
ids that land in the existing `tasks.skills` pinned-skills column and
resolve with §4.1's after-colon fallback (Task 7).

**Read first:**
- 025 §4.1 (the format's normative definition — the metadata-key table,
  numbering, heading and cycle rules, the skill-identifier fallback), §5
  (the two-acts table and the nullable `plan_doc`), §7 (the query table),
  §10 (the verb set), §13 AC2/AC4
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
- **Content inside `## Tasks` that is not a task subsection is an accept
  error.** §4.1 says the section holds "nothing but" task subsections
  without ruling on violations; dropping stray prose silently would lose
  text the author meant to mint, so the parser refuses — the same stance
  §4.1 takes on unknown metadata keys.
- **An exact registry name beats the after-colon fallback.** §4.1 orders
  resolution ("falls back") without saying what wins when a pin could match
  both a qualified and an unqualified registry row; exact-first keeps the
  qualified row authoritative for its own name.

## File structure

| File | Responsibility |
|---|---|
| `internal/designdoc/plantasks.go` (+ test) (new) | plan-task-format parsing |
| `internal/store/docs.go` | the plan branch of `AcceptDoc`; `NeedsPlanning`/`NeedsExecution` queries |
| `internal/store/tasks.go`, `ranking.go` | `planBlockedCondition`; `TaskInput.PlanDoc`; filters |
| `internal/store/brief.go` (+ test) | `ResolvePins` after-colon fallback (§4.1) |
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
| a fence carrying only `kind` | defaults: `priority: medium`, no skills, no blockers |
| a task section with **no** yaml fence, or a fence without `kind` | error naming the task — `kind` is required and has no default (§4.1's key table) |
| `blockedBy: [1]` on Task 2 | `BlockedBy = []int{1}` |
| `blockedBy` naming a number not in the file, or a task's own number | error naming the task and the number |
| `blockedBy` forming a cycle (Task 1 ← 2, Task 2 ← 1) | error naming the tasks on the cycle |
| `kind: review` or `kind: spike` | error naming the task and the value — plans mint only the §4.1 subset; likewise a kind or priority outside its set entirely |
| numbers `1, 3` (gap), `2, 1` (order), `1, 1` (duplicate), or starting at 2 | error — `N` runs 1, 2, 3… in document order without gaps (§4.1) |
| a `###` heading in `## Tasks` not matching `Task <N> — <title>` — hyphen or en dash for the em dash, or an empty title | error quoting the heading and the expected form |
| non-blank content between `## Tasks` and its first task heading | error — see the decisions section |
| two `## Tasks` sections | error — exactly one (§4.1) |
| task headings outside a `## Tasks` section | ignored — only the `## Tasks` section enumerates |
| a plan with a `## Tasks` section but no task headings, or no `## Tasks` section | error: "plan defines no tasks" — accepting it would mint nothing and break AC2's ⟺ |

- [ ] **Step 2: Implement**

```go
// PlanTask is one task definition in a plan document's ## Tasks section —
// the plan task format 025 §5 mints from (docs/authoring-design-docs.md
// carries the canonical definition).
type PlanTask struct {
	Number    int
	Title     string   // heading text after "Task N — "
	Body      string   // the section's own content, yaml fence excluded
	Kind      string   // required; one of the §4.1 mintable four
	Priority  string   // default "medium"
	Skills    []string // plugin:skill ids the executing agent loads
	BlockedBy []int    // task numbers within this plan
}

var planTaskHeadingRE = regexp.MustCompile(`^Task\s+(\d+)\s+—\s+(.+)$`)

// planMintableKinds is the subset of task kinds a plan may mint (025 §4.1):
// review tasks are created by the review lifecycle and spikes are inputs to
// planning, so neither is plan-declarable.
var planMintableKinds = []string{"feature", "bug", "chore", "design"}

// PlanTasks extracts the task definitions the accept transaction mints:
// the `### Task N — Title` sections under the single `## Tasks` heading,
// each opening with a yaml metadata fence (kind required; priority, skills,
// blockedBy optional). Validation errors name the task; the numbers run
// 1, 2, 3… in document order without gaps, and blockedBy must be acyclic
// (025 §4.1).
func PlanTasks(d *Document) ([]PlanTask, error)
```

Parsing notes: the section body must open (blank lines aside) with a fenced
block whose info string is `yaml` — decode with `yaml.v3` +
`KnownFields(true)` so a typoed key is an error, matching
`parseFrontmatter`'s stance; a fence appearing later is ordinary body
content. The section body minus the fence becomes `Body`. Heading matching
requires the em dash — §4.1 makes it part of the format — and the near-miss
error quotes the offending heading and the expected `Task <N> — <title>`
shape, so the fix is obvious at accept time. Cycle detection is a
depth-first walk over `BlockedBy` after the defs are collected; the error
lists the numbers on the cycle. The test asserts `planMintableKinds` equals
`ns.TaskKinds` minus `review` and `spike`, so the subset cannot drift from
part 1's generated list.

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
- A plan whose body fails `PlanTasks` (no tasks, dangling or cyclic
  `blockedBy`, missing or unmintable kind) refuses to accept with the
  parser's error; status stays `draft`.
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

### Task 7 — Resolve qualified skill pins with the after-colon fallback

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

§4.1's skill-identifier rule: a pin in `plugin:skill` form resolves exactly
when the registry name is qualified, and falls back to the segment after the
colon where the registry name is unqualified; an unresolvable pin stays a
brief warning, never a failure (016 §3). `ResolvePins` today matches exactly
only, so a plugin-qualified pin against an unqualified registry name warns
spuriously — in the brief and in `POST /api/v1/skills/recommend`, which
share it. Independent of the doc machinery, so no `blockedBy`.

**Files:**
- Modify: `internal/store/brief.go` (`ResolvePins`, line ~106)
- Test: `internal/store/brief_test.go`

- [ ] **Step 1: Write the failing tests**

Registry fixture: unqualified `test-driven-development`, qualified
`superpowers:writing-plans`.

- Pin `superpowers:test-driven-development` resolves to
  `test-driven-development` via the fallback, content included, no warning.
- Pin `superpowers:writing-plans` resolves exactly; an exact hit never
  consults the fallback.
- Pin `other:absent` (no row under either name) warns
  `pinned skill not found: other:absent`, exactly as today.
- Pin `absent` (no colon) gets no fallback and warns.
- The fallback lands only on unqualified names: with registry `b:x` and no
  `x`, pin `a:x` warns rather than matching `b:x`.
- A fallback hit on a soft-deleted skill returns content plus the
  removed-from-source warning, matching exact-match behaviour.

- [ ] **Step 2: Implement**

In `ResolvePins`, after the exact `SkillsByNames` pass: collect the
still-unresolved pins containing a colon, `strings.Cut` each after its first
colon, query `SkillsByNames` once with the deduped suffixes, and accept a
hit only for the pin's own suffix. Returned `Skill` entries carry the
registry name; warnings keep naming the pin as written. Extend the function
comment with the 025 §4.1 citation. No new metrics: no new store operation,
and the warning surface is unchanged.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store/ -run TestResolvePins -count=1 -v
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
5. §4.1 conformance is total: one `## Tasks` section, contiguous numbering
   from 1, em-dash headings, required `kind` from the mintable four, acyclic
   `blockedBy`, unknown metadata keys refused — and qualified skill pins
   resolve through the after-colon fallback everywhere `ResolvePins` is
   consulted. `wl:requiresSkill` itself is already minted in
   `ns/ontology.ttl` with 016 §1's amendment note; its graph projection is
   out of scope (025 §12).
