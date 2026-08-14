---
status: draft
covers:
- docs/specs/036-one-model-across-packages.md#sec-2
- docs/specs/036-one-model-across-packages.md#sec-3
- docs/specs/036-one-model-across-packages.md#sec-4
- docs/specs/036-one-model-across-packages.md#sec-6
---
# One model across packages — the `internal/model` migration

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse the `store.Task` / `api.taskJSON` / `cli.Task` triplicate —
and the 80-odd sibling pairs behind it — into one declaration per wire shape in
a new `internal/model` package, per ADR 036.

**Architecture:** `internal/model` is a new leaf package of plain structs with
JSON tags and stdlib-only imports. `internal/store` scans rows into model
types and returns them, `internal/api` encodes them directly, `internal/cli`
decodes into them, `internal/ui` embeds them in its view types. Every
`to*JSON` conversion function and every `cli.*` hand-mirror of an `api.*JSON`
struct is deleted rather than rewritten. The migration is staged by shape
category — entities, then response projections, then request bodies — and each
task leaves the tree building and green on its own.

**Tech Stack:** Go 1.26, module `github.com/sunstoneinstitute/worklode`. No new
dependencies; `internal/model` adds none by construction.

## Global constraints

- **`internal/model` imports the standard library only.** No pgx, no
  `net/http`, no templ, no other `internal/*` package. Task 1 adds the test
  that enforces this; do not weaken it later to unblock a move.
- **Field names are wire names.** `store.Task.ProjectID` becomes
  `model.Task.Project` with `json:"project"`. The column name stays in the
  scan expression, where it already is.
- **Move declarations verbatim.** Field sets, types, tag strings, and doc
  comments transfer unchanged unless this plan names a specific change. A
  refactor that also edits the wire shape is two changes wearing one commit.
- **JSON output must not change.** The API is the contract; this migration is
  invisible to any client. `e2e/` is the check that proves it — run
  `go test -race -count=1 -tags e2e ./e2e/` before each commit that touches a
  handler.
- **Having a `json:` tag does not mean a type crosses the wire.**
  `api.oauthState` and `api.cliIntent` carry tags because they are serialized
  into a signed cookie and a state parameter, not into a response body. They
  stay in `internal/api` (ADR 036 §3, "transport internals"). The test in
  Task 6 allowlists them by name.
- Store tests need Postgres (`TEST_POSTGRES_DSN`, default
  `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`) and
  skip silently without it unless `CI` is set. A green run with no Postgres
  proves nothing about a task that renames a store field.
- Run `go build ./...` and `go vet ./...` before every commit. Never put
  `Co-authored-by` or any agent advertisement in a commit message.

## Non-goals

- **The timeline wire shape.** `api.timelineEntry` holds
  `obj map[string]any`, so a timeline entry has no typed declaration in either
  package — `cli.TimelineResponse` decodes into `map[string]any` too. Typing
  it is a design change, not a move, and stays out of this plan. File it as a
  follow-up when Task 4 lands.
- Route, handler, permission, or storage changes of any kind.
- Renaming `internal/ui` view types or reworking the templ components beyond
  swapping `store.X` for `model.X`.

## Tasks

### Task 1 — `internal/model`, the Task and Lease shapes, and the import guard

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

This task sets the pattern every later task repeats, so get it exactly right
before moving on. It creates the package, moves the two most-duplicated
shapes, and adds the test that keeps the package a leaf.

**Files**

- Create: `internal/model/model.go` (package doc), `internal/model/task.go`,
  `internal/model/lease.go`, `internal/model/deps_test.go`
- Modify: `internal/store/tasks.go`, `internal/store/leases.go` and every
  store file returning `Task`/`Lease`; `internal/api/tasks.go` (delete
  `taskJSON` and `toTaskJSON`), `internal/api/lifecycle.go` (delete
  `leaseJSON`); `internal/cli/client.go` (delete `Task` and `Lease`);
  `internal/ui/views.go` and `internal/ui/task.templ`

**Procedure**

- [ ] **Step 1: Write the failing import guard**

`internal/model/deps_test.go`:

```go
package model_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestModelImportsStdlibOnly enforces ADR 036 §4: internal/model is a leaf
// every layer can depend on, so it may not reach back into the module or
// pull in a third-party package.
func TestModelImportsStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"github.com/sunstoneinstitute/worklode/internal/model").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		// A stdlib import path has no dot in its first element.
		first, _, _ := strings.Cut(dep, "/")
		if strings.Contains(first, ".") {
			t.Errorf("internal/model must import stdlib only, got %s", dep)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/model/ -run TestModelImportsStdlibOnly -v`
Expected: FAIL — the package does not exist yet
(`no Go files in .../internal/model`).

- [ ] **Step 3: Create the package with `Task` and `Lease`**

`internal/model/model.go` carries the package doc:

```go
// Package model holds every shape that crosses Worklode's HTTP boundary:
// entities, response projections, and request bodies. One declaration per
// shape — internal/store scans into these types, internal/api serializes
// them, internal/cli decodes them, internal/ui embeds them (ADR 036).
//
// This package imports the standard library only, so every layer can depend
// on it and nothing depends back. deps_test.go enforces that.
package model
```

`internal/model/task.go` takes `api.taskJSON`'s declaration verbatim, renamed
and exported:

```go
package model

import "time"

// Task is a unit of work. Concern is "" when the task has none; Assignee is
// "" when the task is unassigned; Skills is never nil (the store guarantees
// an empty slice, so the JSON reads [] rather than null).
type Task struct {
	ID                 string    `json:"id"`
	Project            string    `json:"project"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	Priority           string    `json:"priority"`
	Kind               string    `json:"kind"`
	State              string    `json:"state"`
	Concern            string    `json:"concern"`
	NeedsDecomposition bool      `json:"needs_decomposition"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	Skills             []string  `json:"skills"`
	Assignee           string    `json:"assignee"`
}
```

`internal/model/lease.go` takes `cli.Lease`'s declaration verbatim (identical
to `api.leaseJSON`):

```go
package model

import "time"

// Lease is an actor's exclusive hold on a task, bound to a worktree.
type Lease struct {
	TaskID     string    `json:"task_id"`
	ActorID    string    `json:"actor_id"`
	Worktree   string    `json:"worktree"`
	AcquiredAt time.Time `json:"acquired_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
```

- [ ] **Step 4: Run the guard and watch it pass**

Run: `go test ./internal/model/ -v`
Expected: PASS.

- [ ] **Step 5: Point the store at the model types**

Delete `store.Task` and `store.Lease`. Add `import "…/internal/model"` and
alias nothing — write `model.Task` at every use site. Two mechanical edits
follow from the rename:

1. `t.ProjectID` becomes `t.Project` throughout `internal/store`. The SQL is
   untouched — only the scan destination changes, e.g.
   `rows.Scan(&t.ID, &t.Project, …)` against the same `project_id` column.
2. The nil-slice guarantee moves here. Wherever a `Task` is filled, ensure
   `Skills` is non-nil before returning:

```go
if t.Skills == nil {
	t.Skills = []string{}
}
```

Put that in the shared row-scan helper rather than at each call site, so a new
query cannot forget it.

- [ ] **Step 6: Run the store tests**

Run: `go test ./internal/store/ -count=1`
Expected: PASS. If they skip, Postgres is not reachable — start it and rerun.
A skip here is not a pass.

- [ ] **Step 7: Delete the api and cli duplicates**

In `internal/api`: delete `taskJSON`, `toTaskJSON`, and `leaseJSON`. Every
`toTaskJSON(t)` call becomes plain `t` (the handler now writes the store's
value straight out); every `taskJSON` field reference becomes the model field.
In `internal/cli`: delete `Task` and `Lease`, and replace their uses with
`model.Task` / `model.Lease`. Do not add a type alias to soften the diff — an
alias is the duplicate under another name.

- [ ] **Step 8: Point `internal/ui` at the model**

`internal/ui/views.go` swaps its `internal/store` import for `internal/model`:
`TaskView.Task` becomes `model.Task` and `TaskView.Holder` becomes
`*model.Lease`. Update the header comment — it currently says view types may
embed `internal/store` types; it now says they embed `internal/model` types,
and cites ADR 036 §3 for why `BoardView` and friends stay ui-local. Leave
`BoardItem` alone; Task 4 handles it.

- [ ] **Step 9: Verify the wire is byte-identical**

Run: `go build ./... && go vet ./... && go test ./internal/... -count=1`
Run: `go test -race -count=1 -tags e2e ./e2e/`
Expected: PASS. The e2e suite reads real JSON off real routes — it is the
evidence that the response bodies did not move.

- [ ] **Step 10: Commit**

```bash
git add internal/model internal/store internal/api internal/cli internal/ui
git commit -m "Introduce internal/model with the Task and Lease shapes"
```

### Task 2 — Move the remaining entities

```yaml
kind: chore
priority: high
skills: [ ]
blockedBy: [1]
```

Same procedure as Task 1, applied to every remaining shape that names a thing
rather than a query result. For each pair below: move the `api` declaration
into `internal/model` under the exported name, delete the `api` type and its
`to*JSON` converter, delete the `cli` twin, and repoint `internal/store`.

**Pairs** (`api` type → `cli` twin → `model` name):

| `internal/api` | `internal/cli` | `internal/model` |
|---|---|---|
| `projectJSON` | `Project` | `Project` |
| `actorJSON` | `Actor` | `Actor` |
| `agentSessionJSON` | `AgentSession` | `AgentSession` |
| `issueJSON` | `Issue` | `Issue` |
| `skillJSON` | `Skill` | `Skill` |
| `skillMatchJSON` | `SkillMatch` | `SkillMatch` |
| `pinnedSkillJSON` | `PinnedSkill` | `PinnedSkill` |
| `recommendationJSON` | `SkillRecommendation` | `SkillRecommendation` |
| `docJSON` | `Doc` | `Doc` |
| `docSectionJSON` | `DocSection` | `DocSection` |
| `docEdgeJSON` | `DocEdge` | `DocEdge` |
| `docUpsertJSON` | `DocUpsert` | `DocUpsert` |
| `runtimeEventJSON` | `RuntimeEvent` | `RuntimeEvent` |
| `repoJSON` | `RepoMapping` | `RepoMapping` |
| `usageBucketJSON` | `SessionUsageBucket` | `SessionUsageBucket` |
| `deliverableJSON` | — (no client yet) | `Deliverable` |

**Where the two sides disagree, the `api` declaration wins** — it is what the
server actually emits. Note any field the `cli` twin was missing in the commit
message; that is a client bug this migration fixes, and reviewers should see
it named.

- [ ] Move one file's worth of pairs at a time (`admin.go` entities, then
      `skills.go`, then `docs.go`, then `agentsessions.go`), building after
      each so a compile error points at one group.
- [ ] `internal/ui/views.go`: `ProjectsView.Projects` becomes
      `[]model.Project`.
- [ ] Run: `go build ./... && go test ./internal/... -count=1`
- [ ] Run: `go test -race -count=1 -tags e2e ./e2e/`
- [ ] Commit: `git commit -m "Move the remaining entity shapes to internal/model"`

### Task 3 — Move the task-shaped response projections

```yaml
kind: chore
priority: high
skills: [ ]
blockedBy: [2]
```

The brief and task-detail cluster — where the drift in ADR 036 §1 was found,
so this is the task that pays for the plan.

**Pairs:**

| `internal/api` | `internal/cli` | `internal/model` |
|---|---|---|
| `briefJSON` | `Brief` | `Brief` |
| `briefBlockerJSON` | `BriefBlocker` | `BriefBlocker` |
| `taskDetailJSON` | `TaskDetail` | `TaskDetail` |
| `edgeOut` | `TaskEdgeOut` | `TaskEdgeOut` |
| `edgeIn` | `TaskEdgeIn` | `TaskEdgeIn` |
| `parentRefJSON` | `TaskParent` | `TaskParent` |
| `progressJSON` | `TaskProgress` | `TaskProgress` |
| `hierarchyJSON` | `TaskHierarchy` | `TaskHierarchy` |
| `decomposeResponse` | `DecomposeResponse` | `DecomposeResponse` |
| `taskPickJSON` | `ClaimNextPick` | `ClaimNextPick` |
| `leasePickJSON` | `ClaimNextPickLease` | `ClaimNextPickLease` |
| — | `ClaimResponse` | `ClaimResponse` |
| — | `TaskListResponse` | `TaskListResponse` |
| — | `ClaimNextResponse` | `ClaimNextResponse` |

The last three have no named `api` type — the handler writes an anonymous
struct or a `map[string]any`. Give each the `cli` declaration and make the
handler encode the model type, so the response shape becomes checkable at
compile time on both sides.

- [ ] `model.Brief` takes `api.briefJSON`'s field order, with `Parent` in its
      `api` position. The `cli` twin had it last; ordering does not change the
      JSON, and one order is the point.
- [ ] `model.TaskDetail` keeps the embedded `Task` and the inline anonymous
      `Edges` struct exactly as `cli.TaskDetail` declares it.
- [ ] `internal/ui/views.go`: `TaskView.Progress` becomes
      `model.TaskProgress` (it currently holds `store.HierarchyProgress`).
- [ ] Run: `go build ./... && go test ./internal/... -count=1`
- [ ] Run: `go test -race -count=1 -tags e2e ./e2e/`
- [ ] Commit: `git commit -m "Move the brief and task-detail projections to internal/model"`

### Task 4 — Move the board, cockpit, claim and import projections

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [3]
```

**Pairs:**

| `internal/api` | `internal/cli` | `internal/model` |
|---|---|---|
| `boardResponse` | `BoardResponse` | `BoardResponse` |
| `boardProjectJSON` | `BoardProject` | `BoardProject` |
| `boardTaskJSON` | `BoardTask` | `BoardTask` |
| `holderJSON` | `Holder` | `Holder` |
| `importCounts` | `ImportCounts` | `ImportCounts` |
| `importResponse` | `ImportResult` | `ImportResult` |
| `syncResponse` | `DocSyncReport` | `DocSyncReport` |
| `docSyncResponse` | `DocSyncResult` | `DocSyncResult` |
| `docResultJSON` | — | `DocResult` |
| `projectCostJSON` | `ProjectCost` | `ProjectCost` |
| `projectDayCostJSON` | `CostDay` | `CostDay` |
| `projectCostTotalJSON` | `CostTotals` | `CostTotals` |
| `projectDetailJSON` | `ProjectDetail` | `ProjectDetail` |
| `tokenCountsJSON` | — | `TokenCounts` |

The cockpit projection (`cockpitProjection`, `cockpitProjectJSON`,
`cockpitModeJSON`, `cockpitWork`, `cockpitWorkItem`, `focusJSON`,
`decisionJSON`, `decisionActionJSON`, `evidenceReferenceJSON`,
`evidenceSummary`, `secondaryConcernJSON`, `repositoryJSON`, `actorSummary`)
moves too, under names without the `JSON` suffix. It has no `cli` twin today,
which is exactly why it should live in `model` before one is written by hand.

**`render.go` shrinks to its real job.** `boardView` and `boardItems` exist
only to copy `boardTaskJSON` into `ui.BoardItem` field by field. With
`model.BoardTask` available, `ui.BoardItem` is deleted and `ui.BoardProject`
holds `[]model.BoardTask`; `boardItems` goes with it. What survives in
`render.go` is genuine composition — wrapping model values in `PageProps` and
building `TimelineRow`s. Update `render.go`'s header comment to say that:
it maps model values into ui view types, and no longer maps between two
declarations of the same thing.

- [ ] Move the board group, delete `ui.BoardItem`/`ui.BoardHolder` and
      `boardItems`, and confirm `board.templ` compiles against
      `model.BoardTask` (`go tool templ generate` then `go build ./...`).
- [ ] Move the cockpit group; confirm `cockpit.templ` and
      `cockpit_test.go` still pass.
- [ ] Move the import, doc-sync and cost groups.
- [ ] Run: `go generate ./... && go build ./... && go test ./internal/... -count=1`
- [ ] Run: `go test -race -count=1 -tags e2e ./e2e/`
- [ ] Commit: `git commit -m "Move the board, cockpit and report projections to internal/model"`
- [ ] File the timeline gap in `docs/follow-ups.md`: `api.timelineEntry.obj`
      is `map[string]any`, so timeline entries have no typed wire shape on
      either side — out of scope for ADR 036's move, worth typing later.

### Task 5 — Move the request bodies

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [4]
```

Request bodies cross the boundary in the other direction: `cli` encodes,
`api` decodes. Same rule, same treatment.

**Pairs:**

| `internal/api` | `internal/cli` | `internal/model` |
|---|---|---|
| `createTaskRequest` | `CreateTaskInput` | `CreateTaskInput` |
| `patchTaskRequest` | `EditTaskInput` | `EditTaskInput` |
| `edgeRequest` | `edgeBody` | `EdgeInput` |
| `claimRequest` | — | `ClaimInput` |
| `claimNextRequest` | `ClaimNextInput` | `ClaimNextInput` |
| `renewRequest` | — | `RenewInput` |
| `rebindWorktreeRequest` | — | `RebindWorktreeInput` |
| `assignRequest` | — | `AssignInput` |
| `setSkillsRequest` | — | `SetSkillsInput` |
| `decomposeRequest` | — | `DecomposeInput` |
| `promoteRequest` | `PromoteInput` | `PromoteInput` |
| `importRequest` | `ImportInput` | `ImportInput` |
| `dismissRequest` | — | `DismissInput` |
| `createActorRequest` | `CreateActorInput` | `CreateActorInput` |
| `createProjectRequest` | `CreateProjectInput` | `CreateProjectInput` |
| `patchProjectRequest` | — | `PatchProjectInput` |
| `addRepoRequest` | — | `AddRepoInput` |
| `linkRequest` | — | `LinkInput` |
| `createTokenRequest` | — | `CreateTokenInput` |
| `revokeTokenRequest` | — | `RevokeTokenInput` |
| `createDeliverableRequest` | — | `CreateDeliverableInput` |
| `agentSessionRequest` | — | `AgentSessionInput` |
| `agentSessionEndRequest` | `EndAgentSessionInput` | `EndAgentSessionInput` |
| `runtimeEventRequest` | — | `RuntimeEventInput` |
| `recommendRequest` | — | `RecommendInput` |
| `docSyncRequest` | `DocSyncInput` | `DocSyncInput` |

Two shapes stay in `internal/api` and must not move: `oidcTokenRequest` and
`cliTokenRequest` are auth-endpoint bodies the CLI builds as literals; leaving
them is defensible but inconsistent, so move them too unless a compile problem
argues otherwise, and record the decision either way in the commit message.

- [ ] Where the `cli` input struct lacks JSON tags (`ClaimNextInput`,
      `EditTaskInput`, `DocSyncInput` — the client builds a map instead), the
      `api` declaration's tags win and the client encodes the model struct
      directly. Delete the hand-built `map[string]any` request bodies in
      `internal/cli/client.go` as you go; that is the bug class this task
      removes.
- [ ] Keep pointer fields pointers. `patchTaskRequest` distinguishes "field
      absent" from "field set to empty"; a value field would silently clear
      data on a partial update.
- [ ] Run: `go build ./... && go test ./internal/... -count=1`
- [ ] Run: `go test -race -count=1 -tags e2e ./e2e/`
- [ ] Commit: `git commit -m "Move the request bodies to internal/model"`

### Task 6 — Enforce the rule with a test

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

Without a check, the next handler reintroduces a local DTO and nobody notices
until it drifts. This is the same shape of guard as `NewServer`'s route-table
check: the rule fails the build rather than a review.

**Files:** Create `internal/model/rule_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package model_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// wireTagged reports whether any field of st carries a json tag.
func wireTagged(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		if f.Tag != nil && strings.Contains(f.Tag.Value, `json:"`) {
			return true
		}
	}
	return false
}

// allowed lists the json-tagged structs that legitimately stay outside
// internal/model: transport internals that are serialized into a cookie or a
// state parameter rather than into a response body (ADR 036 §3).
var allowed = map[string]bool{
	"oauthState": true,
	"cliIntent":  true,
}

// TestNoWireStructsOutsideModel enforces ADR 036 §2: a struct with json tags
// in internal/api or internal/cli is a wire shape, and wire shapes have
// exactly one declaration, in internal/model.
func TestNoWireStructsOutsideModel(t *testing.T) {
	fset := token.NewFileSet()
	for _, pkg := range []string{"../api", "../cli"} {
		paths, err := filepath.Glob(filepath.Join(pkg, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", pkg, err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || allowed[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if ok && wireTagged(st) {
					t.Errorf("%s: %s has json tags outside internal/model "+
						"(ADR 036 §2) — move it, or add it to allowed with a reason",
						filepath.Base(path), ts.Name.Name)
				}
				return true
			})
		}
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/model/ -run TestNoWireStructsOutsideModel -v`
Expected: PASS if Tasks 1–5 are complete. Any failure names a shape those
tasks missed — move it rather than allowlisting it. `allowed` grows only for
a type that is serialized somewhere other than an HTTP body, and the entry
gets a comment saying where.

- [ ] **Step 3: Confirm it actually fails on a violation**

Temporarily add a `json:"x"` tag to any struct in `internal/api/server.go`,
rerun, and confirm the test names it. Then revert. A guard nobody has seen
fail is a guard nobody knows works.

- [ ] **Step 4: Full verification**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Run: `go test -race -count=1 -tags e2e ./e2e/`

- [ ] **Step 5: Commit**

```bash
git add internal/model/rule_test.go
git commit -m "Fail the build on a wire struct declared outside internal/model"
```
