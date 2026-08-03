---
status: draft
implements:
  - docs/specs/025-documents-in-the-backbone.md#sec-5
  - docs/specs/025-documents-in-the-backbone.md#sec-6
  - docs/specs/025-documents-in-the-backbone.md#sec-8
  - docs/specs/025-documents-in-the-backbone.md#sec-9
---
# Documents in the backbone 1/4: kinds, containers, ontology

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 4. Task numbers restart at 1 within each part; a
cross-part reference says "part N Task M". Each part must be merged before
the next starts.

- **Part 1 — kinds, containers, ontology (7 tasks):** `ns/` codegen, the kind
  migration (`spec`→`design`, `epic` removed), container guards re-keyed from
  a declared kind to "has children", decompose as the only child-acquisition
  path, and the §8/§4/§3 ontology edits.
  *Checkpoint:* six kinds everywhere, no epic anywhere, hierarchy behaviour
  unchanged for a decomposed task.
- **Part 2 — document store (6 tasks):** `docs`/`doc_sections`/`doc_edges`
  tables, the store layer, the editorial lifecycle with the accept-time
  anchor gate, API + web read surface.
- **Part 3 — plan acceptance (6 tasks):** plans as documents, accept-time
  task minting from the plan task format, `plan_doc`, plan-to-plan `blocks`
  in the ready set, the `lode doc` verbs.
- **Part 4 — corpus cutover (4 tasks):** import the git corpus, delete the
  files, retire the `sec*` scripts and hooks. Gated on an explicit human go
  decision.

**Goal:** Implement 025 §6, §8, §9 and the 018-narrowing half of §5: task
kinds become the six-kind scheme generated from `ns/`, `epic` disappears, and
every container guard applies to *a task that has children* instead of a
declared kind, with `lode task decompose` the only way a task acquires
children.

**Architecture:** `scripts/nsgen.py` (Python, rdflib) makes `ns/concept.ttl`
the enum source: it emits `internal/ns/gen.go`, CI fails on drift, and
`validKinds` derives from the generated list — so the kind swap is one commit
touching the Turtle, the generated code, and the migration together, and
`TestTaskKindsAgreeAcrossSources` never sees the sources disagree. The store's
container predicate changes from `kind = 'epic'` to an EXISTS over `child_of`
edges (indexed by `task_edges_children`); with `Decompose` creating parent-hood
and children in one transaction, the predicate and the column it replaces can
never disagree (025 §5).

**Tech Stack:** Go 1.25+, Postgres via golang-migrate, Python 3 + rdflib
(codegen only — never in a git hook), cobra CLI.

**Spec:** `docs/specs/025-documents-in-the-backbone.md`

**Read first:**
- 025 §5 ("What this leaves of 018"), §6, §8, §13 AC1/AC3
- `internal/store/tasks.go:99-110` (`kindEpic`, `epicForbiddenStates`),
  `:188-221` (`Transition`), `:519-565` (`AddEdge`)
- `internal/store/hierarchy.go` (`checkHierarchy`, `Decompose`),
  `internal/store/hierarchy_resolve.go` (`epicTarget`, `ResolveHierarchy`)
- `internal/store/ranking.go:58-91` (`readyCandidates`),
  `internal/store/leases.go:140-160` (the epic claim guard),
  `internal/store/delivery_resolve.go:78-93`
- `internal/api/tasks.go:18-27` (`validKinds`, `invalidKindMsg`),
  `internal/api/tasks_test.go:632-670` (`TestTaskKindsAgreeAcrossSources`)
- `ns/concept.ttl`, `ns/ontology.ttl:119-132,216-223,343-348`,
  `ns/shapes.ttl:149-175`

**Conventions:**
- `go test ./internal/...`; store and API tests need Postgres with pgvector
  (`TEST_POSTGRES_DSN` overrides the DSN; tests skip silently without it).
- `./scripts/check-migrations.sh --no-fix` after any migration task.
- `riot --validate ns/*.ttl` after any Turtle edit.
- Commit after every task, imperative mood, no trailers.

**Migration numbers are provisional.** `0010` is claimed by
`2026-08-03-spec-shorthand-references.md`; this plan uses `0011`. The
pre-commit collision script renumbers automatically if plans land in a
different order.

**Non-goals:** the document store (parts 2–3); anything in 025 §12; graph
projection of `wl:inProject` (006 §6 owns projection, and no projector is
built); revising the unexecuted plan `2026-07-30-design-documents-as-graph-objects.md`,
whose §8 stance 025 supersedes — flagged for its owner, not fixed here.

---

## What exists vs. what this builds

- Kind enum today: migration `0009` CHECK is
  `('feature','bug','chore','spec','epic','review','spike')`; `validKinds`
  (`internal/api/tasks.go:20`) and `wlc:TaskKind` (`ns/concept.ttl:50-67`)
  carry the same seven. Target (025 §6):
  `('feature','bug','chore','design','review','spike')` — `epic` is dropped
  as a concept, with no structural replacement; a plan is a document, never
  a task.
- Container identity today is declared: `kindEpic` gates `Transition`, `Claim`,
  `readyCandidates`, `ResolveDelivery`, `ResolveHierarchy`, and
  `checkHierarchy` requires an epic parent. `Decompose` flips `kind` to
  `'epic'`.
- Child-acquisition paths today: `Decompose`, `POST /tasks/{id}/edges`
  (`child_of`), `POST /tasks` with `parent`, inbox promote with `parent`,
  `lode task add --parent`, `lode task parent --under`. Target (025 §13 AC3):
  decompose only; edge *removal* (`unparent`) stays as the repair path.
- `ns/ontology.ttl` still declares `wl:Workstream`, `wl:Project` (bounded),
  `wl:OngoingMaintenance`, `wl:inWorkstream`, and no `wl:Plan`;
  `ns/concept.ttl` still carries `wlc:proposed` and `wlc:epic`;
  `ns/shapes.ttl:170` requires `wl:inWorkstream` minCount 1 on Tasks.
- No codegen exists; the enums are hand-mirrored in three places and one test.

---

## Tasks

### Task 1 — Add `ns/` codegen and `internal/ns`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `scripts/nsgen.py`, `internal/ns/gen.go` (generated),
  `internal/ns/ns_test.go`
- Modify: `internal/api/tasks.go:18-27`, `.github/workflows/_lint.yml`

No enum changes in this task — the generator is proven against the *current*
seven kinds and four statuses, so this commit is pure plumbing.

- [ ] **Step 1: Write the generator**

`scripts/nsgen.py` (python3, `#!/usr/bin/env python3`, imports rdflib — this
is a maintainer/CI tool, never a git hook, so the third-party import is fine
where `secfmt.py`'s ban is not negotiable):

- Parse `ns/concept.ttl`. For each scheme in `{wlc:TaskKind,
  wlc:DesignDocStatus}` collect `skos:Concept` members via
  `?c skos:inScheme <scheme>`; order `DesignDocStatus` by
  `wlc:DesignDocStatusOrder`'s `skos:memberList`, sort `TaskKind`
  alphabetically.
- Emit `internal/ns/gen.go`:

```go
// Code generated by scripts/nsgen.py from ns/concept.ttl. DO NOT EDIT.
//
// ns/ owns the shared schema (025 §9); a change here is a change to the
// Turtle first, then `scripts/nsgen.py`, then the migration, in one commit.
package ns

// TaskKinds mirrors wlc:TaskKind and the tasks.kind CHECK constraint.
var TaskKinds = []string{"bug", "chore", "epic", "feature", "review", "spec", "spike"}

// DesignDocStatuses mirrors wlc:DesignDocStatus, in lifecycle order.
var DesignDocStatuses = []string{"draft", "proposed", "accepted", "superseded"}
```

- `--check`: regenerate to memory, byte-compare with the file on disk, exit 1
  with a diff on mismatch, else exit 0. Default (no flag) writes the file.

- [ ] **Step 2: Derive `validKinds` from the generated list**

In `internal/api/tasks.go` replace the literal map and message:

```go
// validKinds mirrors the tasks.kind CHECK constraint and wlc:TaskKind; the
// list is generated from ns/concept.ttl (scripts/nsgen.py), so the three
// sources move together (025 §9).
var validKinds = func() map[string]bool {
	m := make(map[string]bool, len(ns.TaskKinds))
	for _, k := range ns.TaskKinds {
		m[k] = true
	}
	return m
}()

var invalidKindMsg = "invalid kind: must be one of " + strings.Join(ns.TaskKinds, ", ")
```

`invalidKindMsg` changes from `const` to `var`; both use sites
(`tasks.go:102`, `admin.go:627`) compile unchanged. Update any test asserting
the old message text.

- [ ] **Step 3: Add the state-shape drift check (closes a follow-up)**

`docs/follow-ups.md` flags that `wl:taskState`'s `sh:in` list in
`ns/shapes.ttl` duplicates the `tasks.state` enum. Pin it in
`internal/ns/ns_test.go`: read `../../ns/shapes.ttl`, extract the `sh:in`
list on the `wl:taskState` property path with a regexp, and compare it
against the state set derived from `store`'s transitions — export
`store.AllStates()` (keys of a set built from `legalTransitions`) for it.

- [ ] **Step 4: CI**

In `.github/workflows/_lint.yml`, after the `section numbers` step:

```yaml
      - name: ns codegen drift
        run: |
          pip install --quiet rdflib
          ./scripts/nsgen.py --check
```

- [ ] **Step 5: Verify**

```bash
./scripts/nsgen.py && git diff --exit-code internal/ns/gen.go
./scripts/nsgen.py --check
go test ./internal/ns/ ./internal/api/ -run 'TestTaskKinds|TestNs'
```

`TestTaskKindsAgreeAcrossSources` must pass untouched — nothing changed but
the plumbing.

- [ ] **Step 6: Commit**

---

### Task 2 — Re-key the container guards to "has children"

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `internal/store/tasks.go` (`Transition`, doc comments),
  `internal/store/hierarchy.go` (`checkHierarchy`, new `hasChildren`),
  `internal/store/hierarchy_resolve.go`, `internal/store/ranking.go`,
  `internal/store/leases.go`, `internal/store/delivery_resolve.go`
- Test: `internal/store/hierarchy_test.go`, `internal/store/tasks_test.go`

The rename half of 025 §5: every 018 guard survives, applied to a task that
has `child_of` children rather than to `kind = 'epic'`. `kindEpic` itself is
deleted in Task 5; in this task the guards stop consulting it.

- [ ] **Step 1: Write the failing tests**

Rework `internal/store/hierarchy_test.go` so every fixture that today creates
`kind: "epic"` parents instead creates a plain task and gives it children via
`Decompose` or a direct `AddEdge` (`AddEdge` stays exported; Task 4 closes
only the public surfaces, so store tests may call it directly throughout).
New assertions, each currently failing:

| Test | Asserts |
|---|---|
| `TestParentNeverInReadySet` | a `feature` task with one draft child is absent from `readyCandidates` even when ready, unblocked, critical |
| `TestClaimRejectsParent` | `Claim` on a task with children → `ErrBadTransition`, message names children |
| `TestParentForbiddenStates` | a task with children rejects `in_progress → in_review` and every delivery state; a childless task of the same kind does not |
| `TestResolveDeliveryIgnoresParents` | delivery facts on a task with children leave its state alone |
| `TestRollUpAppliesToAnyKind` | roll-up forward/backward works on a `feature` parent exactly as the old epic tests had it |
| `TestChildlessTaskIsOrdinary` | a childless `chore` claims, transitions and delivers normally |

- [ ] **Step 2: Implement**

In `internal/store/hierarchy.go`:

```go
// hasChildren reports whether taskID has at least one child_of child. This
// is the container predicate of 025 §5: a task with children is never
// claimable, never enters a delivery state, and rolls up from its children —
// container-ness follows from the edges, no kind declares it.
func hasChildren(tx *sql.Tx, taskID string) (bool, error)
```

(one `EXISTS` query over `task_edges_children`). Then:

- `tasks.go`: rename `epicForbiddenStates` → `containerForbiddenStates`. In
  `Transition`, replace the `kind == kindEpic` guard: when `from` or `to` is
  in the set, call `hasChildren`; reject with a message naming the roll-up
  rule ("task %s has children: its state follows them"). Drop `kind` from the
  state read.
- `leases.go` `Claim`: replace the kind guard with `hasChildren` (the row is
  already locked `FOR UPDATE`, so the check is race-free against a concurrent
  decompose of the same task).
- `delivery_resolve.go` `ResolveDelivery`: replace `kind == kindEpic` with
  `hasChildren`.
- `hierarchy_resolve.go` `ResolveHierarchy`: delete the `kind != kindEpic`
  arm (keep the `draft` gate). A childless task yields no child states,
  `epicTarget` returns `""`, and the function is a no-op — the predicate is
  the data. Rename `epicTarget` → `rollupTarget`; update comments to say
  *parent*/*container*, citing 025 §5.
- `ranking.go` `readyCandidates`: replace `AND t.kind <> 'epic'` with
  `AND NOT EXISTS (SELECT 1 FROM task_edges c WHERE c.to_task = t.id AND c.type = 'child_of')`
  and update the doc comment.
- `hierarchy.go` `checkHierarchy`: delete the `kind[parent] != kindEpic`
  check (the parent's container-ness now *results from* the edge). Cross-
  project, single-parent, cycle and depth checks stay exactly as they are.
  `AddEdge` no longer needs the `kind` map — drop it.

- [ ] **Step 3: Verify**

```bash
go test ./internal/store/ -count=1
```

Existing tests that asserted "parent must be an epic" now assert the opposite
(any task may be decomposed); fix fixtures, not guards.

- [ ] **Step 4: Commit**

---

### Task 3 — Drop the kind flip from decompose

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/store/hierarchy.go` (`Decompose`)
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests**

- `TestDecomposeKeepsKind`: decomposing a `feature` leaves the parent's kind
  `feature`; children inherit it; no `kind` row appears in `state_log`.
- `TestDecomposeAddsToExistingParent`: decomposing a task that already has
  children *adds* the new children (this replaces the removed
  `task add --parent` path for growing a subtree) instead of erroring.
- Existing rejections still hold: blank titles, active lease, delivery-state
  parent, depth.

- [ ] **Step 2: Implement**

In `Decompose`:

- Delete the `kind == kindEpic` early rejection and the kind-flip `UPDATE` +
  `LogChange` block; keep the `needs_decomposition = false, updated_at = …`
  update (drop `kind` from its SET list).
- Keep everything else: lease rejection, `containerForbiddenStates[state]`
  rejection, the ancestor-depth check, draft children inheriting project,
  priority, concern and kind, the closing `ResolveHierarchy`.
- Update the doc comment: decompose is the **only** path by which a task
  acquires children (025 §13 AC3), and re-running it on a task with children
  grows the same subtree.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store/ -run 'TestDecompose|TestRollUp' -count=1 -v
```

---

### Task 4 — Make decompose the only child-acquisition path

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

**Files:**
- Modify: `internal/api/tasks.go` (`createTask`, `addEdge`, `validEdgeTypes`,
  `listTasks`), `internal/api/admin.go` (inbox promote), `internal/store/tasks.go`
  (`TaskFilter`, `ListTasks`), `internal/cli/client.go`, `internal/cmd/task.go`,
  `internal/cmd/inbox.go`, `internal/cli/render.go`
- Test: `internal/api/tasks_test.go`, `internal/api/hierarchy_test.go`,
  `internal/cmd/task_test.go`

- [ ] **Step 1: Write the failing tests**

| Surface | Expectation |
|---|---|
| `POST /tasks/{id}/edges` type `child_of` | 422, message: children are created by `lode task decompose` |
| `DELETE /tasks/{id}/edges` type `child_of` | still 204 — unparent stays as the repair path |
| `POST /tasks` with `parent` | 400 (unknown field — `readJSON` uses `DisallowUnknownFields`, so removing the struct field is the rejection) |
| inbox promote with `parent` | 400 likewise |
| `GET /tasks?has_children=true` | lists exactly the tasks with children (the `lode task tree` source, replacing `kind=epic`) |

- [ ] **Step 2: Implement**

- API: split `validEdgeTypes` into `addableEdgeTypes = {blocks}` (checked in
  `addEdge`) and keep both types accepted by `removeEdge`. Delete the
  `Parent` field and its pre-check + `AddEdge` call from `createTask`; same
  for the promote handler in `admin.go` (also delete its now-dead
  `kind == "epic"` rejection — that block dies here, ahead of the enum swap).
- Store: `TaskFilter` gains `HasChildren bool`; `ListTasks` adds
  `EXISTS (SELECT 1 FROM task_edges e WHERE e.to_task = tasks.id AND e.type = 'child_of')`
  when set. API `listTasks` wires `has_children=true`.
- CLI: delete `newTaskParentCmd` and the `--parent` flags on `task add` and
  `inbox promote`; keep `unparent`, `tree`, `list --parent`. `task tree`
  (`internal/cmd/task.go:903`) lists via `HasChildren: true` instead of
  `Kind: "epic"`. `internal/cli/client.go`: drop `CreateTaskInput.Parent` and
  the `Parent` method; keep `Unparent`. `render.go`: "no epics" →
  "no tasks with children"; rename `TreeNode.Epic` → `TreeNode.Root`.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/api/ ./internal/cmd/ ./internal/cli/ ./internal/store/ -count=1
```

---

### Task 5 — Swap the task kinds in one commit

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1, 4]
```

**Files:**
- Create: `deploy/base/migrations/0011_document_task_kinds.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`, `ns/concept.ttl`,
  `internal/ns/gen.go` (regenerated), `internal/api/tasks_test.go`,
  `internal/cmd/task.go:92`, `internal/cmd/inbox.go:116` (flag help),
  `internal/store` fixtures still using kind `spec`/`epic`

Everything in this task lands in **one commit**, per 025 §6 — the agreement
test reads all three sources.

- [ ] **Step 1: Update the failing agreement test first**

In `TestTaskKindsAgreeAcrossSources` (`internal/api/tasks_test.go:639`),
replace the literal `kinds` slice with `ns.TaskKinds` and let the test drive:
after the Turtle edit and regeneration it creates a task of every one of the
six kinds through the API (exercising `validKinds` and the CHECK) and
compares the `.ttl` against the same list. Run it now: it fails on the CHECK
(`design` rejected).

- [ ] **Step 2: The migration**

`deploy/base/migrations/0011_document_task_kinds.up.sql`:

```sql
-- Task kinds (docs/specs/025-documents-in-the-backbone.md §6): 'spec' is
-- renamed 'design' (authoring any Worklode document) and 'epic' is removed —
-- container-ness now derives from child_of edges, so no kind replaces it.

-- Explicit data migration, not a hand-wave: rename spec-kind rows, and
-- re-kind any surviving epic rows to 'chore'. 025 §6 expects no epic rows;
-- if any exist their container-ness lives on in their child_of edges, and
-- the nature-of-work kind of a converted container is not mechanically
-- recoverable, so 'chore' is the neutral fallback.
UPDATE tasks SET kind = 'design' WHERE kind = 'spec';
UPDATE tasks SET kind = 'chore'  WHERE kind = 'epic';

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike'));
```

`.down.sql` restores 0009's seven-kind CHECK verbatim and remaps
`design`→`spec`. The epic conversion is one-way (the down migration cannot
know which chores were epics); the down comment says so. Never edit 0009.

List both files in `deploy/base/kustomization.yaml` after the `0009` pair.

- [ ] **Step 3: The Turtle and the regeneration**

In `ns/concept.ttl` `wlc:TaskKind`: delete `wlc:epic` (lines 64-67), rename
`wlc:spec` to `wlc:design` with 025 §6's widened definition:

```turtle
wlc:design a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "design" ;
    skos:definition """Author or revise a Worklode document — spec, ADR, or plan; the document
        produced is reachable via prov:wasGeneratedBy (014 §9). Closes when the document is
        accepted; never an umbrella held open against coverage (025 §6).""" .
```

Update the scheme's header comment (the constraint mirror is now 025 §6, and
no structural member remains). Then:

```bash
./scripts/nsgen.py && riot --validate ns/concept.ttl
```

`validKinds` and `invalidKindMsg` pick the change up from `internal/ns` with
no edit. Update the `--kind` flag help at `internal/cmd/task.go:92` and
`internal/cmd/inbox.go:116` to `feature, bug, chore, design, review, spike`,
and re-kind any remaining test fixtures using `spec`/`epic`.

- [ ] **Step 4: Verify**

```bash
./scripts/check-migrations.sh --no-fix
go test ./internal/... -count=1
go test -race -count=1 -tags e2e ./e2e/   # expected to fail only in hierarchy_test.go — Task 7 fixes it
```

`TestMigrateRoundTrip` exercises the new pair down and up.

- [ ] **Step 5: Commit** (single commit: migration + kustomization + Turtle +
  `gen.go` + help text + tests)

---

### Task 6 — Apply the ontology edits: Project, Workstream, Plan, `proposed`

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [1]
```

**Files:**
- Modify: `ns/ontology.ttl`, `ns/shapes.ttl`, `ns/concept.ttl`,
  `internal/ns/gen.go` (regenerated), any test asserting `proposed`

Pure `ns/` state change; 025 §8 gives the target Turtle verbatim and §4/§3
give the rest. The specs are already amended (025 frontmatter), so per
CLAUDE.md's rule the mirror edit is now due.

- [ ] **Step 1: `ns/ontology.ttl`**

- Delete `wl:Workstream`, `wl:OngoingMaintenance` (lines 119-122, 129-132),
  `wl:inWorkstream` (lines 343-348), and the
  `(wl:Project wl:OngoingMaintenance)` disjointness axiom (line 223).
- Replace `wl:Project` (lines 124-127) and add `wl:inProject` with 025 §8's
  Turtle verbatim (unbounded umbrella over `project_repos`; functional
  Task→Project derived from `tasks.project_id`).
- In the top-level `owl:AllDisjointClasses` (line 216), `wl:Project` takes
  `wl:Workstream`'s slot.
- Add `wl:Plan` beside the `wl:DesignDoc` subclasses (after line 66) with
  025 §4's Turtle verbatim (sibling of DesignDoc, `wl:layer wlc:execution`,
  mutable, anchor-free, acceptance mints its tasks). Add `wl:Plan` to
  `wl:status`'s domain union (line 252) — plans carry an editorial status
  (026 §5). Update the trailing "wl:Plan dropped (014 §2)" comment
  (line ~450) to record the 025 §4 return.

- [ ] **Step 2: `ns/shapes.ttl` and `ns/concept.ttl`**

- Task shape (lines 170-173): `wl:inWorkstream` minCount-1 becomes
  `wl:inProject` `sh:minCount 1 ; sh:maxCount 1`, message citing 025 §8.
- `ns/concept.ttl`: delete `wlc:proposed` (lines 30-31) and drop it from
  `wlc:DesignDocStatusOrder` (line 40) — a document under review is a draft
  with an open review task (025 §3). Update the section comment.

- [ ] **Step 3: Regenerate, validate, verify**

```bash
./scripts/nsgen.py && ./scripts/nsgen.py --check
riot --validate ns/ontology.ttl ns/concept.ttl ns/shapes.ttl
go test ./internal/ns/ ./internal/api/ -count=1
```

`ns.DesignDocStatuses` becomes `draft, accepted, superseded` — part 2's
`docs.status` CHECK is generated from exactly this list.

- [ ] **Step 4: Commit**

---

### Task 7 — Rework the e2e hierarchy loop without epics

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

**Files:**
- Modify: `e2e/hierarchy_test.go`

- [ ] **Step 1: Rework the loop**

`TestHierarchyLoop` keeps its shape (public surfaces only) with the new
semantics: create a `feature` task, `Decompose` it (assert the parent's kind
is **still** `feature` and the children are drafts), assert the parent never
reaches the ready set nor accepts a claim while it has children, close the
children through the normal claim → done path, and watch the parent roll up
to `merged`. Add one step: a second `Decompose` on the same parent adds a
third child and pulls the parent back per the roll-up table.

- [ ] **Step 2: Verify and commit**

```bash
go test -race -count=1 -tags e2e ./e2e/
```

---

## Done when (maps to 025 §13)

1. AC1 first half: `epic` absent and `design` present in the CHECK,
   `validKinds` and `wlc:TaskKind`; all three generated from or pinned to
   `ns/`; `./scripts/nsgen.py --check` green in CI.
2. AC3: `Decompose` is the only path that creates `child_of` edges through
   any public surface, and every 018 guard applies exactly while a task has
   children, with no kind to declare.
3. AC7 (ns half): `wl:Workstream` and `wl:OngoingMaintenance` absent from
   `ns/`; `wl:inProject` declared functional; no sprint term; `riot` clean.
4. `go test ./... ` and the e2e suite green.
