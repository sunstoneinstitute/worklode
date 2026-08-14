---
status: superseded
covers: docs/specs/004-execution-backbone.md
---
# Task Hierarchy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `docs/specs/004-execution-backbone.md`: finish the half-built `child_of` edge with a declared `kind = 'epic'` container, one parent per task, a two-edge depth cap, epics excluded from the ready set, automatic closure roll-up, and `lode task decompose` to close the loop from spec 005.

**Architecture:** Two mechanisms, kept apart. *Progress* (`closed/total` direct children) is derived on read and never stored. *Closure* is stored: `store.ResolveHierarchy` applies the epic roll-up table and is called from the tail of `store.Transition` itself, not from its eleven call sites, so the invariant cannot break by a forgotten caller. Epic identity is declared (`kind = 'epic'`), never inferred from having children, which makes the ready-set exclusion a column predicate and makes conversion an explicit act that validates its preconditions.

**Tech Stack:** Go 1.25+, Postgres (golang-migrate `*.up.sql`/`*.down.sql` files), net/http `ServeMux`, cobra CLI, `html/template` web pages. Store and API tests need Postgres from `docker-compose.yml` (`store.OpenTestStore` skips the test if unreachable and `CI` is unset).

**Read first:** `docs/specs/004-execution-backbone.md` (the spec), `internal/store/tasks.go` (`legalTransitions`, `Transition`, `AddEdge`, `reachesViaChildOf`, `closedStates`), `internal/store/ranking.go:61` (`readyCandidates`), `internal/store/delivery_resolve.go:78` (`ResolveDelivery`), `internal/store/events.go` (`RecordEvent`).

**Conventions:**
- Run `go test ./internal/...` for the unit suite; `go test -tags e2e ./e2e/...` for e2e.
- Commit after every task, imperative mood, **no** `Co-authored-by:` or any other advertising trailer.
- Comments stay short and precise. Do not narrate the change history in a doc comment.
- Every new exported symbol gets a doc comment explaining *why*, matching the density of the surrounding file.

---

## Decisions resolved before writing this plan

The spec left two questions open. Both are now settled, in the spec's own direction:

- **Q018.1 — auto-close:** ship `ResolveHierarchy` as specced. Wrap-up work becomes a child task, not a reason to make closure manual.
- **Q018.2 — `lode task done <epic>`:** an error. `done` is `in_review → merged`, and `in_review` is forbidden for epics, so the kind guard rejects it with a message naming the roll-up rule. No manual override path.

Two rules the spec implies but does not state, adopted here and recorded in Task 14:

- **A parent must already be a declared epic.** `AddEdge` rejects a `child_of` edge whose `to_task` is not `kind = 'epic'` (422). The spec's CLI signature is literally `lode task parent <id> --under <epic>`, and this is what keeps `ResolveHierarchy`'s domain exactly the epic state table. Two supported ways to get an epic: create one (`lode task add --kind epic`) or convert in place (`lode task decompose`). No `--kind` flag is added to `lode task edit`.
- **A direct claim of an epic is rejected too.** The spec's "never claimable" is enforced in `readyCandidates` *and* in `Claim`, because `ready → in_progress` is a legal epic transition (it is the roll-up trigger) and would otherwise let `lode task claim <epic-id>` through.

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0006_task_hierarchy.{up,down}.sql` | `epic` kind, single-parent partial unique index, child-lookup index |
| `internal/store/hierarchy.go` (new) | Everything hierarchy: depth walks, `checkHierarchy`, `ResolveHierarchy`, `epicTarget`, readers (`ParentOf`, `ChildProgress`, `ParentMap`), `Decompose` |
| `internal/store/tasks.go` | `AddEdge` delegates to `checkHierarchy`; `Transition` gains the epic kind guard and the roll-up hook |
| `internal/store/ranking.go` | `readyCandidates` excludes epics |
| `internal/store/leases.go` | `Claim` rejects epics |
| `internal/store/delivery_resolve.go` | `ResolveDelivery` returns early for epics |
| `internal/store/brief.go` | `Brief.Parent`, one hop up |
| `internal/api/tasks.go` | `epic` kind, `parent` on create, `hierarchy` on detail, `parent`/`kind` list filters |
| `internal/api/hierarchy.go` (new) | `POST /api/v1/tasks/{id}/decompose` |
| `internal/api/admin.go` | board tasks carry their parent |
| `internal/api/web.go`, `templates/task.html` | parent, children, progress on the task page |
| `internal/cli/client.go` | wire types and methods for the new endpoints |
| `internal/cli/render.go` | `show` parent/progress lines, `TreeRender`, board grouping |
| `internal/cmd/task.go` | `add --parent`, `parent`, `unparent`, `tree`, `list --parent`, `decompose` |

---

### Task 1: Migration 0006 — epic kind and hierarchy indexes

**Files:**
- Create: `deploy/base/migrations/0006_task_hierarchy.up.sql`
- Create: `deploy/base/migrations/0006_task_hierarchy.down.sql`
- Modify: `deploy/base/kustomization.yaml:13-24` (configMapGenerator file list)
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the up migration**

`deploy/base/migrations/0006_task_hierarchy.up.sql`:

```sql
-- Task hierarchy (docs/specs/004-execution-backbone.md): epics as declared
-- containers, at most one parent per task, indexed child lookups.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic'));

-- A task has at most one parent. Two child_of edges out of one task are legal
-- under the baseline UNIQUE (from_task, to_task, type), and the task page
-- silently keeps whichever was inserted last.
CREATE UNIQUE INDEX task_edges_single_parent
    ON task_edges (from_task) WHERE type = 'child_of';

-- Child lookups (WHERE to_task = $1 AND type = 'child_of') have no usable
-- index: the baseline unique constraint leads with from_task.
CREATE INDEX task_edges_children
    ON task_edges (to_task) WHERE type = 'child_of';
```

- [ ] **Step 2: Write the down migration**

`deploy/base/migrations/0006_task_hierarchy.down.sql`:

```sql
DROP INDEX task_edges_children;
DROP INDEX task_edges_single_parent;

-- Re-adding the four-kind CHECK validates existing rows, so the revert fails
-- loudly if any epic survives rather than leaving an unrepresentable task.
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec'));
```

- [ ] **Step 3: Register both files with kustomize**

In `deploy/base/kustomization.yaml`, append to the `worklode-migrations` `files:` list, after the `0005_delivery` pair:

```yaml
      - migrations/0006_task_hierarchy.up.sql
      - migrations/0006_task_hierarchy.down.sql
```

- [ ] **Step 4: Write the failing test**

Create `internal/store/hierarchy_test.go`:

```go
package store

import (
	"database/sql"
	"testing"
)

// TestMigrationAllowsEpicKind checks the 0006 CHECK change: 'epic' is a legal
// task kind and an unknown kind is still rejected.
func TestMigrationAllowsEpicKind(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	if epic.Kind != "epic" {
		t.Fatalf("kind = %q, want epic", epic.Kind)
	}

	bad := defaultTaskInput()
	bad.Kind = "saga"
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, _ int64) error {
			_, err := CreateTask(tx, taskTestNow, bad)
			return err
		})
	if err == nil {
		t.Fatal("CreateTask with kind=saga succeeded, want a CHECK violation")
	}
}

// TestSingleParentIndex checks that the partial unique index rejects a second
// child_of edge out of one task, whichever parent it points at.
func TestSingleParentIndex(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	epicA := createTask(t, s, taskTestNow, epicInput())
	epicB := createTask(t, s, taskTestNow, epicInput())

	if _, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, epicA.ID, taskTestNow); err != nil {
		t.Fatalf("first parent edge: %v", err)
	}
	_, err := s.DBForTests().Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, 'child_of', $3)`,
		child.ID, epicB.ID, taskTestNow)
	if err == nil {
		t.Fatal("second parent edge succeeded, want a unique violation")
	}
	if !isUniqueViolationOn(err, "task_edges_single_parent") {
		t.Fatalf("error = %v, want a task_edges_single_parent unique violation", err)
	}
}

// epicInput is the shared fixture for a container task.
func epicInput() TaskInput {
	in := defaultTaskInput()
	in.Title = "an epic"
	in.Kind = "epic"
	return in
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestMigrationAllowsEpicKind|TestSingleParentIndex' -v`
Expected: FAIL — `TestMigrationAllowsEpicKind` fails on the baseline `tasks_kind_check`, `TestSingleParentIndex` fails because the second insert succeeds.

(If both tests are skipped, Postgres is unreachable: start it with `docker compose up -d postgres` and re-run.)

- [ ] **Step 6: Run the test to verify it passes**

The migration files written in Steps 1–2 are the implementation; `OpenTestStore` applies them.

Run: `go test ./internal/store/ -run 'TestMigrationAllowsEpicKind|TestSingleParentIndex' -v`
Expected: PASS

- [ ] **Step 7: Verify the down migration reverts cleanly**

Run:
```bash
go run ./cmd/lode migrate --dsn "$TEST_POSTGRES_DSN" --migrations-path deploy/base/migrations 2>/dev/null || true
kustomize build deploy/base | grep -c 0006_task_hierarchy
```
Expected: `2` (both files land in the ConfigMap). The migrate command is best-effort here; the kustomize count is the assertion.

- [ ] **Step 8: Commit**

```bash
git add deploy/base/migrations/0006_task_hierarchy.up.sql \
        deploy/base/migrations/0006_task_hierarchy.down.sql \
        deploy/base/kustomization.yaml internal/store/hierarchy_test.go
git commit -m "Add migration 0006: epic kind, single-parent and child indexes"
```

---

### Task 2: Store — hierarchy invariants on `AddEdge`

Enforce, for every `child_of` edge: the parent is a declared epic, both endpoints share a project, the child has no parent yet, no cycle, and the resulting chain is at most two edges.

**Files:**
- Create: `internal/store/hierarchy.go`
- Modify: `internal/store/tasks.go:405-442` (`AddEdge`)
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/hierarchy_test.go`:

```go
// addEdge drives AddEdge through RecordEvent and returns its error.
func addEdge(t *testing.T, s *Store, from, to, typ string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, _ int64) error {
			return AddEdge(tx, taskTestNow, from, to, typ)
		})
	return err
}

func TestAddEdgeRejectsNonEpicParent(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	parent := createTask(t, s, taskTestNow, defaultTaskInput())

	err := addEdge(t, s, child.ID, parent.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestAddEdgeRejectsSecondParent(t *testing.T) {
	s := openTaskStore(t)
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	epicA := createTask(t, s, taskTestNow, epicInput())
	epicB := createTask(t, s, taskTestNow, epicInput())

	if err := addEdge(t, s, child.ID, epicA.ID, "child_of"); err != nil {
		t.Fatalf("first parent: %v", err)
	}
	err := addEdge(t, s, child.ID, epicB.ID, "child_of")
	if !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("error = %v, want ErrEdgeExists", err)
	}
	// The baseline duplicate-edge rule still applies to the same pair.
	if err := addEdge(t, s, child.ID, epicA.ID, "child_of"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge error = %v, want ErrEdgeExists", err)
	}
}

func TestAddEdgeRejectsCrossProject(t *testing.T) {
	s := openTaskStore(t)
	if err := s.CreateProject(t.Context(), "other", "Other", "OTH"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic := createTask(t, s, taskTestNow, epicInput())
	otherIn := defaultTaskInput()
	otherIn.ProjectID = "other"
	child := createTask(t, s, taskTestNow, otherIn)

	err := addEdge(t, s, child.ID, epic.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestAddEdgeEnforcesDepthCap(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, epicInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	deep := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, mid.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("depth 1: %v", err)
	}
	if err := addEdge(t, s, leaf.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("depth 2: %v", err)
	}
	// A third level is one edge too many.
	err := addEdge(t, s, deep.ID, leaf.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// TestAddEdgeDepthCapCountsSubtree checks that adopting a task that already
// has children counts the whole resulting chain, not just the new edge.
func TestAddEdgeDepthCapCountsSubtree(t *testing.T) {
	s := openTaskStore(t)
	top := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, epicInput())
	sub := createTask(t, s, taskTestNow, epicInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, leaf.ID, sub.ID, "child_of"); err != nil {
		t.Fatalf("leaf under sub: %v", err)
	}
	if err := addEdge(t, s, sub.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("sub under mid: %v", err)
	}
	// mid already carries a 2-deep subtree; hanging it under top makes 3.
	err := addEdge(t, s, mid.ID, top.ID, "child_of")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

// TestAddEdgeStillRejectsCycles checks the pre-existing cycle guard survives
// the rewrite: a parent cannot become a child of its own descendant.
func TestAddEdgeStillRejectsCycles(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, epicInput())

	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child under epic: %v", err)
	}
	err := addEdge(t, s, epic.ID, child.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("error = %v, want ErrCycle", err)
	}
}
```

Add `"errors"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestAddEdge' -v`
Expected: FAIL — every new case succeeds where it should be rejected, except the cycle test which already passes.

- [ ] **Step 3: Write `internal/store/hierarchy.go`**

```go
// Task hierarchy (docs/specs/004-execution-backbone.md): epics are declared
// containers, a task has at most one parent, and a chain is at most
// maxHierarchyDepth edges deep. Progress is derived on read; closure is
// stored, one transition per event, by ResolveHierarchy.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// maxHierarchyDepth caps a child_of chain at two edges (epic -> task ->
// subtask). The brief is a bounded payload and the walks that feed roll-up and
// breadcrumbs are unbounded without a cap.
const maxHierarchyDepth = 2

// ancestorHops returns the number of child_of edges between id and the root of
// its hierarchy (0 for a task with no parent). The visited set keeps the walk
// terminating even if the stored graph already contains a cycle.
func ancestorHops(tx *sql.Tx, id string) (int, error) {
	visited := map[string]bool{id: true}
	hops, cur := 0, id
	for {
		var parent string
		err := tx.QueryRow(
			`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`,
			cur).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return hops, nil
		}
		if err != nil {
			return 0, fmt.Errorf("walk parents of %s: %w", cur, err)
		}
		if visited[parent] {
			return hops, nil
		}
		visited[parent] = true
		hops++
		cur = parent
	}
}

// descendantDepth returns the length of the longest child_of chain below id
// (0 for a task with no children).
func descendantDepth(tx *sql.Tx, id string) (int, error) {
	visited := map[string]bool{id: true}
	depth := 0
	frontier := []string{id}
	for len(frontier) > 0 {
		var next []string
		for _, cur := range frontier {
			kids, err := childIDs(tx, cur)
			if err != nil {
				return 0, err
			}
			for _, k := range kids {
				if !visited[k] {
					visited[k] = true
					next = append(next, k)
				}
			}
		}
		if len(next) > 0 {
			depth++
		}
		frontier = next
	}
	return depth, nil
}

// childIDs returns the ids of a task's direct children.
func childIDs(tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.Query(
		`SELECT from_task FROM task_edges WHERE to_task = $1 AND type = 'child_of'`, id)
	if err != nil {
		return nil, fmt.Errorf("walk children of %s: %w", id, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan child of %s: %w", id, err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("walk children of %s: %w", id, err)
	}
	return out, nil
}

// checkHierarchy validates a proposed "child child_of parent" edge against the
// spec-018 invariants. project and kind carry both endpoints' columns, already
// read by AddEdge.
func checkHierarchy(tx *sql.Tx, child, parent string, project, kind map[string]string) error {
	if kind[parent] != "epic" {
		return fmt.Errorf("parent %s is a %s, not an epic: %w", parent, kind[parent], ErrInvalidInput)
	}
	if project[child] != project[parent] {
		return fmt.Errorf("cross-project edge %s (%s) child_of %s (%s): %w",
			child, project[child], parent, project[parent], ErrInvalidInput)
	}

	var existing string
	err := tx.QueryRow(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`,
		child).Scan(&existing)
	if err == nil {
		return fmt.Errorf("task %s already has parent %s: %w", child, existing, ErrEdgeExists)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check parent of %s: %w", child, err)
	}

	reaches, err := reachesViaChildOf(tx, parent, child)
	if err != nil {
		return err
	}
	if reaches {
		return fmt.Errorf("edge %s child_of %s: %w", child, parent, ErrCycle)
	}

	above, err := ancestorHops(tx, parent)
	if err != nil {
		return err
	}
	below, err := descendantDepth(tx, child)
	if err != nil {
		return err
	}
	if depth := above + 1 + below; depth > maxHierarchyDepth {
		return fmt.Errorf("edge %s child_of %s would make a %d-edge chain (max %d): %w",
			child, parent, depth, maxHierarchyDepth, ErrInvalidInput)
	}
	return nil
}
```

- [ ] **Step 4: Rewrite `AddEdge` to use it**

In `internal/store/tasks.go`, replace the body of `AddEdge` (the endpoint-existence loop, the `child_of` cycle check, and the insert's error mapping) so it reads:

```go
// AddEdge inserts a typed edge between two existing tasks inside the given
// transaction. Self-edges are rejected for both types. A child_of edge must
// also satisfy the spec-018 hierarchy invariants (see checkHierarchy): an epic
// parent, one project, one parent per task, no cycle, and at most
// maxHierarchyDepth edges. A missing endpoint returns ErrNotFound.
func AddEdge(tx *sql.Tx, now time.Time, fromTask, toTask, typ string) error {
	if typ != "child_of" && typ != "blocks" {
		return fmt.Errorf("unknown edge type %q: %w", typ, ErrInvalidInput)
	}
	if fromTask == toTask {
		return fmt.Errorf("self-edge %s %s %s not allowed: %w", fromTask, typ, toTask, ErrInvalidInput)
	}
	project := map[string]string{}
	kind := map[string]string{}
	for _, id := range []string{fromTask, toTask} {
		var p, k string
		err := tx.QueryRow(`SELECT project_id, kind FROM tasks WHERE id = $1`, id).Scan(&p, &k)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("check task %s: %w", id, err)
		}
		project[id], kind[id] = p, k
	}
	if typ == "child_of" {
		if err := checkHierarchy(tx, fromTask, toTask, project, kind); err != nil {
			return err
		}
	}
	_, err := tx.Exec(
		`INSERT INTO task_edges (from_task, to_task, type, created_at) VALUES ($1, $2, $3, $4)`,
		fromTask, toTask, typ, now.UTC(),
	)
	if err != nil {
		// The partial unique index is the backstop for a second parent racing
		// checkHierarchy's read; both report the same shape.
		if isUniqueViolationOn(err, "task_edges_single_parent") {
			return fmt.Errorf("task %s already has a parent: %w", fromTask, ErrEdgeExists)
		}
		if isUniqueViolation(err) {
			return fmt.Errorf("edge %s %s %s: %w", fromTask, typ, toTask, ErrEdgeExists)
		}
		return fmt.Errorf("insert edge %s %s %s: %w", fromTask, typ, toTask, err)
	}
	return nil
}
```

Leave `reachesViaChildOf` where it is — `checkHierarchy` still calls it.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestAddEdge|TestSingleParent|TestMigration' -v`
Expected: PASS (all six new cases plus Task 1's two).

- [ ] **Step 6: Run the full store suite for regressions**

Run: `go test ./internal/store/`
Expected: PASS. Existing `child_of` tests that parented tasks under non-epics will now fail — fix them by making the parent fixture `Kind: "epic"`, not by weakening the guard.

- [ ] **Step 7: Commit**

```bash
git add internal/store/hierarchy.go internal/store/tasks.go internal/store/hierarchy_test.go internal/store/tasks_test.go
git commit -m "Enforce epic parent, one parent, one project, and a depth cap on child_of"
```

---

### Task 3: Store — hierarchy readers

Derived progress and the one-hop parent, plus `parent`/`kind` filters on `ListTasks`. Nothing here writes.

**Files:**
- Modify: `internal/store/hierarchy.go`
- Modify: `internal/store/tasks.go:43-48` (`TaskFilter`), `:349-399` (`ListTasks`)
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/hierarchy_test.go`:

```go
func TestChildProgress(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	var kids []*Task
	for i := 0; i < 3; i++ {
		k := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, k.ID, epic.ID, "child_of"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		kids = append(kids, k)
	}

	got, err := s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{Closed: 0, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}

	walkTo(t, s, kids[0].ID, "merged")
	if err := transition(t, s, taskTestNow, kids[1].ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	got, err = s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{Closed: 2, Total: 3}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestChildProgressNoChildren(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	got, err := s.ChildProgress(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if want := (HierarchyProgress{}); got != want {
		t.Fatalf("progress = %+v, want %+v", got, want)
	}
}

func TestParentOf(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	got, err := s.ParentOf(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("ParentOf: %v", err)
	}
	if got == nil || got.ID != epic.ID || got.Title != epic.Title || got.State != epic.State {
		t.Fatalf("parent = %+v, want id/title/state of %s", got, epic.ID)
	}

	root, err := s.ParentOf(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("ParentOf root: %v", err)
	}
	if root != nil {
		t.Fatalf("parent of a root task = %+v, want nil", root)
	}
}

func TestListTasksFilterParentAndKind(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	loose := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	kids, err := s.ListTasks(t.Context(), TaskFilter{Parent: epic.ID})
	if err != nil {
		t.Fatalf("ListTasks parent: %v", err)
	}
	if len(kids) != 1 || kids[0].ID != child.ID {
		t.Fatalf("children = %v, want [%s]", ids(kids), child.ID)
	}

	epics, err := s.ListTasks(t.Context(), TaskFilter{Kind: "epic"})
	if err != nil {
		t.Fatalf("ListTasks kind: %v", err)
	}
	if len(epics) != 1 || epics[0].ID != epic.ID {
		t.Fatalf("epics = %v, want [%s]", ids(epics), epic.ID)
	}
	_ = loose
}

func ids(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestChildProgress|TestParentOf|TestListTasksFilter' -v`
Expected: FAIL — `undefined: HierarchyProgress`, `s.ChildProgress`, `s.ParentOf`, and unknown `TaskFilter` fields.

- [ ] **Step 3: Add `closedStateSet` and the readers**

Append to `internal/store/hierarchy.go`:

```go
// closedStateSet mirrors the closedStates SQL tuple for in-Go checks. Both
// must list the same states.
var closedStateSet = map[string]bool{
	"merged": true, "deployed_dev": true, "deployed_prod": true,
	"released": true, "abandoned": true,
}

// HierarchyProgress is an epic's derived roll-up: how many of its direct
// children are closed, out of how many. It is computed on read and never
// stored — there is no resolver, no migration, and no event-log noise behind
// it.
type HierarchyProgress struct {
	Closed int
	Total  int
}

// ChildProgress returns the closed/total counts over taskID's direct children.
// A task with no children reports a zero value.
func (s *Store) ChildProgress(ctx context.Context, taskID string) (HierarchyProgress, error) {
	var p HierarchyProgress
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE t.state IN `+closedStates+`)
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of'`, taskID).Scan(&p.Total, &p.Closed)
	if err != nil {
		return HierarchyProgress{}, fmt.Errorf("child progress of %s: %w", taskID, err)
	}
	return p, nil
}

// ParentOf returns taskID's parent, or nil when it has none. Only ID, Title,
// and State are populated: one hop up is all any caller needs, and the full
// ancestry is unbounded.
func (s *Store) ParentOf(ctx context.Context, taskID string) (*Task, error) {
	var p Task
	err := s.db.QueryRowContext(ctx,
		`SELECT t.id, t.title, t.state
		   FROM task_edges e JOIN tasks t ON t.id = e.to_task
		  WHERE e.from_task = $1 AND e.type = 'child_of'`, taskID).Scan(&p.ID, &p.Title, &p.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("parent of %s: %w", taskID, err)
	}
	return &p, nil
}

// ParentMap returns child id -> parent id for every child_of edge in a project
// (every project when projectID is ""). One query, so a board can group an
// epic's children under it without a lookup per task.
func (s *Store) ParentMap(ctx context.Context, projectID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.from_task, e.to_task
		   FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.type = 'child_of' AND ($1 = '' OR t.project_id = $1)`, projectID)
	if err != nil {
		return nil, fmt.Errorf("parent map: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, fmt.Errorf("scan parent map row: %w", err)
		}
		out[child] = parent
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("parent map: %w", err)
	}
	return out, nil
}
```

Note: `strings` and `time` are imported by `hierarchy.go` for later tasks; if the compiler complains about unused imports at this point, add them in the task that first needs them.

- [ ] **Step 4: Add the `TaskFilter` fields**

In `internal/store/tasks.go`, extend `TaskFilter`:

```go
// TaskFilter narrows ListTasks. Zero-valued fields do not filter. Parent
// selects the direct children of one task.
type TaskFilter struct {
	Project  string
	States   []string
	Priority string
	Kind     string
	Parent   string
}
```

and in `ListTasks`, after the `Priority` clause and before the `len(conds) > 0` check:

```go
	if f.Kind != "" {
		args = append(args, f.Kind)
		conds = append(conds, fmt.Sprintf(`kind = $%d`, len(args)))
	}
	if f.Parent != "" {
		args = append(args, f.Parent)
		conds = append(conds, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM task_edges e
			          WHERE e.from_task = tasks.id AND e.to_task = $%d AND e.type = 'child_of')`,
			len(args)))
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestChildProgress|TestParentOf|TestListTasksFilter' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/hierarchy.go internal/store/tasks.go internal/store/hierarchy_test.go
git commit -m "Add derived hierarchy readers and parent/kind task filters"
```

---

### Task 4: Store — epics are never claimable and never delivered

Four guards, all small: the ready-set predicate, the direct-claim rejection, the `Transition` kind guard, and `ResolveDelivery`'s early return.

**Files:**
- Modify: `internal/store/ranking.go:61-70` (`readyCandidates`)
- Modify: `internal/store/leases.go:127-133` (`Claim`'s row lock)
- Modify: `internal/store/tasks.go:154-180` (`Transition`)
- Modify: `internal/store/delivery_resolve.go:78-85` (`ResolveDelivery`)
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/hierarchy_test.go`:

```go
// TestEpicNeverInReadySet checks that an epic stays out of the ranked pickup
// set even when it is ready, unblocked, and top-ranked by every other factor.
func TestEpicNeverInReadySet(t *testing.T) {
	s := openTaskStore(t)
	in := epicInput()
	in.Priority = "critical"
	epic := createTask(t, s, taskTestNow, in)
	plain := createTask(t, s, taskTestNow, defaultTaskInput())

	got, err := s.readyCandidates(t.Context(), "")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	if len(got) != 1 || got[0].ID != plain.ID {
		t.Fatalf("candidates = %v, want [%s] (epic %s excluded)", ids(got), plain.ID, epic.ID)
	}
}

// TestClaimRejectsEpic checks the direct-claim hole: ready -> in_progress is a
// legal epic transition (it is the roll-up trigger), so Claim needs its own
// guard.
func TestClaimRejectsEpic(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, err := s.Claim(t.Context(), epic.ID, "stig", "wt-1", time.Hour)
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("error = %v, want ErrBadTransition", err)
	}
}

// TestEpicForbiddenStates checks the kind guard on both ends of a transition:
// an epic can never enter a delivery state, and `lode task done` (in_review ->
// merged) reports the roll-up rule rather than a from-state mismatch.
func TestEpicForbiddenStates(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	if err := transition(t, s, taskTestNow, epic.ID, "ready", "in_progress"); err != nil {
		t.Fatalf("ready -> in_progress: %v", err)
	}
	err := transition(t, s, taskTestNow, epic.ID, "in_progress", "in_review")
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("in_review error = %v, want ErrBadTransition", err)
	}
	if !strings.Contains(err.Error(), "epic") {
		t.Fatalf("error %q does not name the epic rule", err)
	}
	err = transition(t, s, taskTestNow, epic.ID, "in_review", "merged")
	if !errors.Is(err, ErrBadTransition) || !strings.Contains(err.Error(), "epic") {
		t.Fatalf("done error = %v, want an epic ErrBadTransition", err)
	}
	// The roll-up terminal is still reachable.
	if err := transition(t, s, taskTestNow, epic.ID, "in_progress", "merged"); err != nil {
		t.Fatalf("in_progress -> merged: %v", err)
	}
}

// TestResolveDeliveryIgnoresEpics checks that an epic with commit and deploy
// facts attributed to it is left alone.
func TestResolveDeliveryIgnoresEpics(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "delivery", nil,
		func(tx *sql.Tx, eventID int64) error {
			if err := RecordTaskCommit(tx, taskTestNow, epic.ID, "o/r", "abc", "branch_push"); err != nil {
				return err
			}
			if _, err := RecordMainCommit(tx, taskTestNow, "o/r", "abc"); err != nil {
				return err
			}
			return ResolveDelivery(tx, taskTestNow, epic.ID, "o/r", eventID)
		})
	if err != nil {
		t.Fatalf("ResolveDelivery: %v", err)
	}
	got, err := s.GetTask(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state = %s, want ready (an epic has no commit)", got.State)
	}
}
```

Add `"strings"` and `"time"` to the test file's imports. Check the exact names and signatures of `RecordTaskCommit` / `RecordMainCommit` in `internal/store/delivery.go` before running, and adjust the call in the last test to match; the assertion (epic stays `ready`) is what matters.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestEpicNeverInReadySet|TestClaimRejectsEpic|TestEpicForbiddenStates|TestResolveDeliveryIgnoresEpics' -v`
Expected: FAIL — the epic appears in the ready set, the claim succeeds, `in_review` is accepted, and the epic advances to `merged` on delivery facts.

- [ ] **Step 3: Exclude epics from the ready set**

In `internal/store/ranking.go`, add one predicate to `readyCandidates` and extend its doc comment:

```go
// readyCandidates returns every task eligible for pickup: state ready, not an
// epic, not needs_decomposition, unleased, and not blocked by an open 'blocks'
// edge from a task that is not in a closed state. An empty projectID matches
// every project. Epics are excluded because the worktree is the unit of
// Worklode work and a container has nothing to check out (spec 018).
func (s *Store) readyCandidates(ctx context.Context, projectID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixedTaskColumns("t")+` FROM tasks t
		WHERE t.state = 'ready'
		  AND t.kind <> 'epic'
		  AND NOT t.needs_decomposition
		  ...
```

(Leave the rest of the query untouched.)

- [ ] **Step 4: Reject a direct claim of an epic**

In `internal/store/leases.go`, widen the locking read in `Claim` and add the guard immediately after it:

```go
			// Lock the task row first so concurrent claims serialize here.
			var state, kind string
			if err := tx.QueryRow(
				`SELECT state, kind FROM tasks WHERE id = $1 FOR UPDATE`, taskID,
			).Scan(&state, &kind); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
				}
				return fmt.Errorf("lock task %s: %w", taskID, err)
			}
			// An epic has nothing to check out; decomposition work that needs a
			// worktree is a child task (spec 018).
			if kind == "epic" {
				return fmt.Errorf("task %s is an epic and cannot be claimed: %w", taskID, ErrBadTransition)
			}
```

- [ ] **Step 5: Add the kind guard to `Transition`**

In `internal/store/tasks.go`, add above `Transition`:

```go
// epicForbiddenStates are the delivery states an epic can never occupy. They
// are earned by observed deploy facts about a specific commit (spec 011) and
// an epic has no commit. Checked on both ends of a transition so `lode task
// done` on an epic reports the roll-up rule instead of a from-state mismatch.
var epicForbiddenStates = map[string]bool{
	"in_review": true, "deployed_dev": true, "deployed_prod": true, "released": true,
}
```

and replace the state read inside `Transition` with:

```go
	var current, kind string
	err := tx.QueryRow(`SELECT state, kind FROM tasks WHERE id = $1`, taskID).Scan(&current, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get task %s state: %w", taskID, err)
	}
	if kind == "epic" && (epicForbiddenStates[from] || epicForbiddenStates[to]) {
		return fmt.Errorf("task %s is an epic: its state follows its children, so it cannot move %s -> %s: %w",
			taskID, from, to, ErrBadTransition)
	}
	if current != from {
		return fmt.Errorf("task %s is in state %s, not %s: %w", taskID, current, from, ErrBadTransition)
	}
```

Update `Transition`'s doc comment to mention the epic restriction in one sentence.

- [ ] **Step 6: Return early from `ResolveDelivery` for epics**

In `internal/store/delivery_resolve.go`, at the very top of `ResolveDelivery`:

```go
func ResolveDelivery(tx *sql.Tx, now time.Time, taskID, repo string, eventID int64) error {
	// An epic has no commit; its state is its children's (spec 018). Checked
	// explicitly rather than relying on the commit join never matching.
	var kind string
	if err := tx.QueryRow(`SELECT kind FROM tasks WHERE id = $1`, taskID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task %s: %w", taskID, ErrNotFound)
		}
		return fmt.Errorf("get task %s kind: %w", taskID, err)
	}
	if kind == "epic" {
		return nil
	}

	landed, err := LandedMainID(tx, taskID, repo)
	...
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestEpicNeverInReadySet|TestClaimRejectsEpic|TestEpicForbiddenStates|TestResolveDeliveryIgnoresEpics' -v`
Expected: PASS

- [ ] **Step 8: Run the full store suite**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/store/ranking.go internal/store/leases.go internal/store/tasks.go \
        internal/store/delivery_resolve.go internal/store/hierarchy_test.go
git commit -m "Keep epics out of the ready set, out of claims, and out of delivery states"
```

---

### Task 5: Store — `ResolveHierarchy` and the `Transition` hook

**Files:**
- Modify: `internal/store/hierarchy.go`
- Modify: `internal/store/tasks.go` (`Transition`'s tail)
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests for the pure target function**

Append to `internal/store/hierarchy_test.go`:

```go
func TestEpicTarget(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		want   string
	}{
		{"no children", nil, ""},
		{"all draft or ready", []string{"draft", "ready"}, "ready"},
		{"one started", []string{"ready", "in_progress"}, "in_progress"},
		{"one landed, one open", []string{"merged", "ready"}, "in_progress"},
		{"all closed, one delivered", []string{"merged", "abandoned"}, "merged"},
		{"all abandoned", []string{"abandoned", "abandoned"}, "abandoned"},
		{"all delivered", []string{"merged", "deployed_prod"}, "merged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := epicTarget(tc.states); got != tc.want {
				t.Fatalf("epicTarget(%v) = %q, want %q", tc.states, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Write the failing tests for the resolver and the hook**

Append:

```go
// epicWithChildren builds an epic with n ready children and returns both.
func epicWithChildren(t *testing.T, s *Store, n int) (*Task, []*Task) {
	t.Helper()
	epic := createTask(t, s, taskTestNow, epicInput())
	var kids []*Task
	for i := 0; i < n; i++ {
		k := createTask(t, s, taskTestNow, defaultTaskInput())
		if err := addEdge(t, s, k.ID, epic.ID, "child_of"); err != nil {
			t.Fatalf("child %d: %v", i, err)
		}
		kids = append(kids, k)
	}
	return epic, kids
}

func stateOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	task, err := s.GetTask(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTask %s: %v", id, err)
	}
	return task.State
}

// TestRollUpForward: the first child to start moves the epic off ready, and
// the last child to close moves it to merged.
func TestRollUpForward(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)

	if err := transition(t, s, taskTestNow, kids[0].ID, "ready", "in_progress"); err != nil {
		t.Fatalf("start child 0: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "in_progress" {
		t.Fatalf("epic = %s, want in_progress", got)
	}

	walkTo(t, s, kids[0].ID, "merged")
	if got := stateOf(t, s, epic.ID); got != "in_progress" {
		t.Fatalf("epic = %s, want in_progress with one child still open", got)
	}
	walkTo(t, s, kids[1].ID, "merged")
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}
}

// TestRollUpZeroChildren: an epic with no children never moves. It is a
// modelling mistake, not a completed epic.
func TestRollUpZeroChildren(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "rollup", nil,
		func(tx *sql.Tx, eventID int64) error {
			return ResolveHierarchy(tx, taskTestNow, epic.ID, eventID)
		})
	if err != nil {
		t.Fatalf("ResolveHierarchy: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "ready" {
		t.Fatalf("epic = %s, want ready", got)
	}
}

func TestRollUpAllAbandoned(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)
	for _, k := range kids {
		if err := transition(t, s, taskTestNow, k.ID, "ready", "abandoned"); err != nil {
			t.Fatalf("abandon %s: %v", k.ID, err)
		}
	}
	if got := stateOf(t, s, epic.ID); got != "abandoned" {
		t.Fatalf("epic = %s, want abandoned", got)
	}
}

func TestRollUpMixedAbandonedAndDelivered(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 2)
	walkTo(t, s, kids[0].ID, "merged")
	if err := transition(t, s, taskTestNow, kids[1].ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged (some of the epic landed)", got)
	}
}

// TestRollUpReopen: a child returning to ready puts a closed epic back to
// ready. Asymmetric roll-up produces boards that lie.
func TestRollUpReopen(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 1)
	walkTo(t, s, kids[0].ID, "merged")
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}
	if err := transition(t, s, taskTestNow, kids[0].ID, "merged", "ready"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := stateOf(t, s, epic.ID); got != "ready" {
		t.Fatalf("epic = %s, want ready", got)
	}
}

// TestRollUpAttribution: the parent's state_log row carries the child's event
// id, so the timeline explains itself with no synthetic event.
func TestRollUpAttribution(t *testing.T) {
	s := openTaskStore(t)
	epic, kids := epicWithChildren(t, s, 1)

	var eventID int64
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, id int64) error {
			eventID = id
			return Transition(tx, taskTestNow, kids[0].ID, "ready", "in_progress", id)
		})
	if err != nil {
		t.Fatalf("start child: %v", err)
	}

	var got int64
	if err := s.DBForTests().QueryRow(
		`SELECT event_id FROM state_log WHERE entity_id = $1 ORDER BY id DESC LIMIT 1`,
		epic.ID).Scan(&got); err != nil {
		t.Fatalf("read epic state_log: %v", err)
	}
	if got != eventID {
		t.Fatalf("epic state_log event_id = %d, want the child's %d", got, eventID)
	}
}

// TestRollUpDepth2Recursion: a subtask closing resolves its task, which
// resolves the epic, in one transaction.
func TestRollUpDepth2Recursion(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, epicInput())
	leaf := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("mid under epic: %v", err)
	}
	if err := addEdge(t, s, leaf.ID, mid.ID, "child_of"); err != nil {
		t.Fatalf("leaf under mid: %v", err)
	}

	walkTo(t, s, leaf.ID, "merged")
	if got := stateOf(t, s, mid.ID); got != "merged" {
		t.Fatalf("mid = %s, want merged", got)
	}
	if got := stateOf(t, s, epic.ID); got != "merged" {
		t.Fatalf("epic = %s, want merged", got)
	}
}
```

Before running, confirm the state-log table and column names (`state_log`, `entity_id`, `event_id`) against `internal/store/changes.go` and `deploy/base/migrations/0001_baseline.up.sql`, and adjust the query in `TestRollUpAttribution` to match. Do not change what it asserts.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run 'TestEpicTarget|TestRollUp' -v`
Expected: FAIL — `undefined: epicTarget`, `undefined: ResolveHierarchy`, and every epic stays in `ready`.

- [ ] **Step 4: Implement `epicTarget` and `ResolveHierarchy`**

Append to `internal/store/hierarchy.go`:

```go
// epicTarget returns the state the spec-018 roll-up table implies for an epic
// whose direct children are in the given states, or "" when no roll-up applies.
// An epic with no children never moves: that is a modelling mistake, not a
// completed epic. All-abandoned rolls up to abandoned rather than merged —
// treating abandonment as delivery would report cancelled work as shipped.
func epicTarget(states []string) string {
	if len(states) == 0 {
		return ""
	}
	closed, abandoned, started := 0, 0, 0
	for _, st := range states {
		if closedStateSet[st] {
			closed++
			if st == "abandoned" {
				abandoned++
			}
		}
		if st == "in_progress" || st == "in_review" {
			started++
		}
	}
	switch {
	case closed == len(states) && abandoned == len(states):
		return "abandoned"
	case closed == len(states):
		return "merged"
	case started > 0 || closed > 0:
		return "in_progress"
	default:
		return "ready"
	}
}

// ResolveHierarchy moves parentID to the state its children imply, per the
// spec-018 roll-up table, inside the given transaction. Non-epics and draft
// epics are left alone: draft -> ready is a manual publish, not a roll-up.
//
// A closed epic whose children reopened routes through ready, the only edge
// out of a closed state, so the reopen shows in the timeline as a reopen.
// Both transitions carry the triggering child's eventID, which is the correct
// attribution for a derived move.
func ResolveHierarchy(tx *sql.Tx, now time.Time, parentID string, eventID int64) error {
	var state, kind string
	err := tx.QueryRow(`SELECT state, kind FROM tasks WHERE id = $1`, parentID).Scan(&state, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %s: %w", parentID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get parent %s: %w", parentID, err)
	}
	if kind != "epic" || state == "draft" {
		return nil
	}

	rows, err := tx.Query(
		`SELECT t.state FROM task_edges e JOIN tasks t ON t.id = e.from_task
		  WHERE e.to_task = $1 AND e.type = 'child_of'`, parentID)
	if err != nil {
		return fmt.Errorf("children of %s: %w", parentID, err)
	}
	var states []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return fmt.Errorf("scan child state of %s: %w", parentID, err)
		}
		states = append(states, st)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("children of %s: %w", parentID, err)
	}
	rows.Close()

	target := epicTarget(states)
	if target == "" || target == state {
		return nil
	}
	if !legalTransitions[[2]string{state, target}] {
		if !legalTransitions[[2]string{state, "ready"}] {
			return nil
		}
		if err := Transition(tx, now, parentID, state, "ready", eventID); err != nil {
			return err
		}
		state = "ready"
		if state == target || !legalTransitions[[2]string{state, target}] {
			return nil
		}
	}
	return Transition(tx, now, parentID, state, target, eventID)
}

// resolveParent rolls the task's parent, if it has one, up to the state its
// children imply. Transition calls this rather than its eleven call sites
// doing so: hooking each caller would leave the invariant one forgotten call
// site away from breaking. Recursion terminates on the depth cap — a subtask
// resolves its task, the task resolves the epic, the epic has no parent.
func resolveParent(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
	var parent string
	err := tx.QueryRow(
		`SELECT to_task FROM task_edges WHERE from_task = $1 AND type = 'child_of'`,
		taskID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("parent of %s: %w", taskID, err)
	}
	return ResolveHierarchy(tx, now, parent, eventID)
}
```

- [ ] **Step 5: Hook it into `Transition`**

In `internal/store/tasks.go`, replace `Transition`'s trailing `return LogChange(...)` with:

```go
	if err := LogChange(tx, "task", taskID, eventID,
		map[string]string{"field": "state", "old": from, "new": to}); err != nil {
		return err
	}
	return resolveParent(tx, now, taskID, eventID)
```

and add one sentence to its doc comment: `A task with a parent rolls that parent up in the same transaction (see resolveParent).`

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestEpicTarget|TestRollUp' -v`
Expected: PASS

- [ ] **Step 7: Run the full suite**

Run: `go test ./internal/...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/store/hierarchy.go internal/store/tasks.go internal/store/hierarchy_test.go
git commit -m "Roll an epic up to its children's state from inside Transition"
```

---

### Task 6: Store — `Brief.Parent`, exactly one hop up

**Files:**
- Modify: `internal/store/brief.go:18-58`
- Test: `internal/store/brief_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/brief_test.go`:

```go
func TestBriefParent(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("child_of: %v", err)
	}

	b, err := s.Brief(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if b.Parent == nil || b.Parent.ID != epic.ID || b.Parent.Title != epic.Title {
		t.Fatalf("parent = %+v, want %s", b.Parent, epic.ID)
	}
	if b.Parent.Body != "" {
		t.Fatalf("parent body = %q, want empty (one hop carries id, title, state only)", b.Parent.Body)
	}

	root, err := s.Brief(t.Context(), epic.ID)
	if err != nil {
		t.Fatalf("Brief root: %v", err)
	}
	if root.Parent != nil {
		t.Fatalf("parent of a root task = %+v, want nil", root.Parent)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestBriefParent -v`
Expected: FAIL — `b.Parent undefined`.

- [ ] **Step 3: Add the field and populate it**

In `internal/store/brief.go`, add to the `Brief` struct, after `OpenBlockers`:

```go
	Parent             *Task    // the task's epic, or nil; only ID/Title/State are populated
```

and extend the struct's doc comment with: `Parent is exactly one hop up — an agent should know its task belongs to "Delivery lifecycle" without spelunking, while the full ancestry and the sibling list are both unbounded and stay out.`

In `Brief`, after the lease lookup:

```go
	parent, err := s.ParentOf(ctx, taskID)
	if err != nil {
		return nil, err
	}

	return &Brief{
		Task:         *t,
		Body:         t.Body,
		Branch:       BranchFor(t),
		OpenBlockers: blockers,
		Parent:       parent,
		Lease:        lease,
	}, nil
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/store/ -run TestBriefParent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/brief.go internal/store/brief_test.go
git commit -m "Carry the parent one hop up in a task brief"
```

---

### Task 7: Store — `Decompose`

**Files:**
- Modify: `internal/store/hierarchy.go`
- Test: `internal/store/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/hierarchy_test.go`:

```go
// decompose drives Decompose through RecordEvent.
func decompose(t *testing.T, s *Store, id string, titles []string) ([]Task, error) {
	t.Helper()
	var kids []Task
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.decomposed", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			kids, err = Decompose(tx, taskTestNow, id, titles, "stig", eventID)
			return err
		})
	return kids, err
}

func TestDecompose(t *testing.T) {
	s := openTaskStore(t)
	in := defaultTaskInput()
	in.Kind = "bug"
	in.Priority = "high"
	in.Concern = "security"
	parent := createTask(t, s, taskTestNow, in)

	flag := true
	if _, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.updated", nil,
		func(tx *sql.Tx, _ int64) error {
			return UpdateTaskFields(tx, taskTestNow, parent.ID, nil, nil, nil, nil, &flag)
		}); err != nil {
		t.Fatalf("set needs_decomposition: %v", err)
	}

	kids, err := decompose(t, s, parent.ID, []string{"Phase one", "Phase two"})
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	for _, k := range kids {
		if k.State != "draft" {
			t.Fatalf("child %s state = %s, want draft", k.ID, k.State)
		}
		if k.Priority != "high" || k.Concern != "security" || k.ProjectID != parent.ProjectID {
			t.Fatalf("child %s did not inherit project/priority/concern: %+v", k.ID, k)
		}
		if k.Kind != "bug" {
			t.Fatalf("child %s kind = %s, want the parent's pre-conversion bug", k.ID, k.Kind)
		}
	}

	got, err := s.GetTask(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Kind != "epic" {
		t.Fatalf("parent kind = %s, want epic", got.Kind)
	}
	if got.NeedsDecomposition {
		t.Fatal("parent still flagged needs_decomposition")
	}
	if got.ID != parent.ID {
		t.Fatalf("parent id changed to %s: decompose must keep the id and every reference to it", got.ID)
	}

	children, err := s.ListTasks(t.Context(), TaskFilter{Parent: parent.ID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("wired children = %d, want 2", len(children))
	}
}

func TestDecomposeRejectsLeasedTask(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := s.Claim(t.Context(), parent.ID, "stig", "wt-1", time.Hour); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, err := decompose(t, s, parent.ID, []string{"A"})
	if !errors.Is(err, ErrLeased) {
		t.Fatalf("error = %v, want ErrLeased", err)
	}
}

func TestDecomposeRejectsEmptyAndBlankTitles(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := decompose(t, s, parent.ID, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty error = %v, want ErrInvalidInput", err)
	}
	if _, err := decompose(t, s, parent.ID, []string{"A", "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank-title error = %v, want ErrInvalidInput", err)
	}
}

// TestDecomposeRespectsDepthCap: a task that is already a child cannot be
// decomposed further without exceeding the two-edge cap.
func TestDecomposeRespectsDepthCap(t *testing.T) {
	s := openTaskStore(t)
	epic := createTask(t, s, taskTestNow, epicInput())
	mid := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, mid.ID, epic.ID, "child_of"); err != nil {
		t.Fatalf("mid under epic: %v", err)
	}
	if _, err := decompose(t, s, mid.ID, []string{"A"}); err != nil {
		t.Fatalf("decompose at depth 1 should be allowed: %v", err)
	}

	deeper, err := s.ListTasks(t.Context(), TaskFilter{Parent: mid.ID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if _, err := decompose(t, s, deeper[0].ID, []string{"B"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("decompose at depth 2 error = %v, want ErrInvalidInput", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run TestDecompose -v`
Expected: FAIL — `undefined: Decompose`.

- [ ] **Step 3: Implement `Decompose`**

Append to `internal/store/hierarchy.go`:

```go
// Decompose converts parentID into an epic and creates its children in one
// transaction: kind becomes 'epic', needs_decomposition clears, and each title
// becomes a draft child inheriting the parent's project, priority, concern,
// and pre-conversion kind. This is what makes the spec-005 needs_decomposition
// gate actionable — an oversized task becomes its own tracking task plus the
// pieces, in place, keeping its id and every reference to it.
//
// Rejected when the parent holds an active lease (decomposing work someone is
// holding is a coordination bug), when the parent sits deep enough that its
// children would exceed maxHierarchyDepth, and from the delivery states an
// epic can never occupy.
func Decompose(tx *sql.Tx, now time.Time, parentID string, titles []string, createdBy string, eventID int64) ([]Task, error) {
	if len(titles) == 0 {
		return nil, fmt.Errorf("decompose %s: at least one child title is required: %w",
			parentID, ErrInvalidInput)
	}
	for _, title := range titles {
		if strings.TrimSpace(title) == "" {
			return nil, fmt.Errorf("decompose %s: child titles must not be blank: %w",
				parentID, ErrInvalidInput)
		}
	}

	var projectID, priority, state, kind string
	var concern sql.NullString
	err := tx.QueryRow(
		`SELECT project_id, priority, state, kind, concern FROM tasks WHERE id = $1 FOR UPDATE`,
		parentID).Scan(&projectID, &priority, &state, &kind, &concern)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %s: %w", parentID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("lock task %s: %w", parentID, err)
	}
	if epicForbiddenStates[state] {
		return nil, fmt.Errorf("task %s is in state %s and cannot become an epic: %w",
			parentID, state, ErrBadTransition)
	}

	var one int
	err = tx.QueryRow(
		`SELECT 1 FROM leases WHERE task_id = $1 AND released_at IS NULL`, parentID).Scan(&one)
	if err == nil {
		return nil, fmt.Errorf("task %s: %w", parentID, ErrLeased)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check active lease on %s: %w", parentID, err)
	}

	above, err := ancestorHops(tx, parentID)
	if err != nil {
		return nil, err
	}
	if above+1 > maxHierarchyDepth {
		return nil, fmt.Errorf("task %s is %d level(s) deep; its children would exceed the max of %d: %w",
			parentID, above, maxHierarchyDepth, ErrInvalidInput)
	}

	childKind := kind
	if childKind == "epic" {
		childKind = "feature"
	}
	if _, err := tx.Exec(
		`UPDATE tasks SET kind = 'epic', needs_decomposition = false, updated_at = $1 WHERE id = $2`,
		now.UTC(), parentID); err != nil {
		return nil, fmt.Errorf("convert task %s to epic: %w", parentID, err)
	}
	if err := LogChange(tx, "task", parentID, eventID,
		map[string]string{"field": "kind", "old": kind, "new": "epic"}); err != nil {
		return nil, err
	}

	children := make([]Task, 0, len(titles))
	for _, title := range titles {
		child, err := CreateTask(tx, now, TaskInput{
			ProjectID: projectID,
			Title:     title,
			Priority:  priority,
			Kind:      childKind,
			Concern:   concern.String,
			CreatedBy: createdBy,
			Draft:     true,
		})
		if err != nil {
			return nil, err
		}
		if err := AddEdge(tx, now, child.ID, parentID, "child_of"); err != nil {
			return nil, err
		}
		children = append(children, *child)
	}

	// The fresh children are all draft, so this only pulls an epic that was
	// mid-flight back to where its children put it.
	if err := ResolveHierarchy(tx, now, parentID, eventID); err != nil {
		return nil, err
	}
	return children, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -run TestDecompose -v`
Expected: PASS

- [ ] **Step 5: Run the full store suite**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/hierarchy.go internal/store/hierarchy_test.go
git commit -m "Add Decompose: convert a task into an epic plus its children in place"
```

---

### Task 8: API — epic kind, `parent` on create, `hierarchy` on detail, list filters

**Files:**
- Modify: `internal/api/tasks.go:18-20` (`validKinds`), `:61-135` (create), `:147-195` (detail), `:197-225` (list)
- Test: `internal/api/hierarchy_test.go` (new)

- [ ] **Step 1: Write the failing tests**

Create `internal/api/hierarchy_test.go`:

```go
package api_test

import (
	"net/http"
	"testing"
)

// createEpic creates a container task through the API and returns its id.
func createEpic(t *testing.T, h http.Handler, token, project, title string) string {
	t.Helper()
	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": project, "title": title, "priority": "medium", "kind": "epic",
	})
	return got["id"].(string)
}

func TestCreateTaskWithEpicKind(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Container", "priority": "medium", "kind": "epic",
	})
	if got["kind"] != "epic" {
		t.Fatalf("kind = %v, want epic", got["kind"])
	}
}

func TestCreateTaskWithParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")

	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+child["id"].(string), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d", rr.Code)
	}
	detail := decodeMap(t, rr)
	hier := detail["hierarchy"].(map[string]any)
	parent := hier["parent"].(map[string]any)
	if parent["id"] != epic {
		t.Fatalf("parent = %v, want %s", parent["id"], epic)
	}
}

// TestCreateTaskWithUnknownParentCreatesNothing checks the single-transaction
// promise: a rejected parent must not leave an unparented child behind.
func TestCreateTaskWithUnknownParentCreatesNothing(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Orphan", "priority": "medium", "kind": "feature",
		"parent": "WL-999",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	list := doReq(t, h, "GET", "/api/v1/tasks?project=proj", token, nil)
	tasks := decodeMap(t, list)["tasks"].([]any)
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0 (the create must have rolled back)", len(tasks))
	}
}

func TestTaskDetailProgress(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")
	for _, title := range []string{"A", "B"} {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
			"parent": epic,
		})
	}

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+epic, token, nil)
	hier := decodeMap(t, rr)["hierarchy"].(map[string]any)
	progress := hier["progress"].(map[string]any)
	if progress["total"].(float64) != 2 || progress["closed"].(float64) != 0 {
		t.Fatalf("progress = %v, want 0/2", progress)
	}
	if hier["parent"] != nil {
		t.Fatalf("parent = %v, want null for a root epic", hier["parent"])
	}
}

func TestSecondParentIsConflict(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epicA := createEpic(t, h, token, "proj", "A")
	epicB := createEpic(t, h, token, "proj", "B")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epicA,
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+child["id"].(string)+"/edges", token,
		map[string]any{"to": epicB, "type": "child_of"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestCrossProjectParentIsUnprocessable(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	epic := createEpic(t, h, token, "proj", "Container")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "other", "title": "Piece", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+child["id"].(string)+"/edges", token,
		map[string]any{"to": epic, "type": "child_of"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestListTasksByParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks?parent="+epic, token, nil)
	tasks := decodeMap(t, rr)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["id"] != child["id"] {
		t.Fatalf("children = %v, want [%v]", tasks, child["id"])
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks?kind=epic", token, nil)
	tasks = decodeMap(t, rr)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["id"] != epic {
		t.Fatalf("epics = %v, want [%s]", tasks, epic)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestCreateTaskWithEpicKind|TestCreateTaskWithParent|TestCreateTaskWithUnknownParent|TestTaskDetailProgress|TestSecondParent|TestCrossProjectParent|TestListTasksByParent' -v`
Expected: FAIL — `kind: epic` is rejected 422, there is no `parent` field, and there is no `hierarchy` object.

- [ ] **Step 3: Accept the epic kind**

In `internal/api/tasks.go`:

```go
var validKinds = map[string]bool{
	"feature": true, "bug": true, "chore": true, "spec": true, "epic": true,
}
```

and update the create handler's message to `"invalid kind: must be feature, bug, chore, spec, or epic"`.

- [ ] **Step 4: Accept `parent` on create**

Add the field to `createTaskRequest`:

```go
	Parent   string `json:"parent"`
```

and inside the `RecordEvent` callback in `createTask`, after `created = t`:

```go
				if req.Parent != "" {
					// Same transaction as the insert: there is no window where
					// the child exists unparented.
					if err := store.AddEdge(tx, s.st.Now(), t.ID, req.Parent, "child_of"); err != nil {
						return err
					}
				}
				return nil
```

- [ ] **Step 5: Add `hierarchy` to the task detail**

In `internal/api/tasks.go`, next to `taskDetailJSON`:

```go
// parentRefJSON is the one-hop-up projection of a task's parent: enough to
// render a breadcrumb without a second request.
type parentRefJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// progressJSON is the derived child roll-up, closed of total direct children.
// Computed on read, never stored.
type progressJSON struct {
	Closed int `json:"closed"`
	Total  int `json:"total"`
}

// hierarchyJSON is the spec-018 hierarchy block on a task detail. parent is
// null for a root task; progress is zeroed for a task with no children.
type hierarchyJSON struct {
	Parent   *parentRefJSON `json:"parent"`
	Progress progressJSON   `json:"progress"`
}
```

Add the field to `taskDetailJSON`:

```go
	Hierarchy hierarchyJSON `json:"hierarchy"`
```

and populate it in `getTask`, after the edges block:

```go
	parent, err := s.st.ParentOf(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	progress, err := s.st.ChildProgress(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp.Hierarchy.Progress = progressJSON{Closed: progress.Closed, Total: progress.Total}
	if parent != nil {
		resp.Hierarchy.Parent = &parentRefJSON{ID: parent.ID, Title: parent.Title, State: parent.State}
	}
```

- [ ] **Step 6: Add the list filters**

In `listTasks`, extend the filter:

```go
	tasks, err := s.st.ListTasks(r.Context(), store.TaskFilter{
		Project:  q.Get("project"),
		States:   states,
		Priority: q.Get("priority"),
		Kind:     q.Get("kind"),
		Parent:   q.Get("parent"),
	})
```

and update the handler's doc comment to `// listTasks handles GET /api/v1/tasks?project=&state=&priority=&kind=&parent=.`

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestCreateTask|TestTaskDetail|TestSecondParent|TestCrossProjectParent|TestListTasks' -v`
Expected: PASS

- [ ] **Step 8: Run the API suite**

Run: `go test ./internal/api/`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/api/tasks.go internal/api/hierarchy_test.go
git commit -m "Expose the task hierarchy over the API: epic kind, parent on create, derived progress"
```

---

### Task 9: API — `POST /api/v1/tasks/{id}/decompose`

**Files:**
- Create: `internal/api/hierarchy.go`
- Modify: `internal/api/server.go:262-279` (route table)
- Test: `internal/api/hierarchy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/api/hierarchy_test.go`:

```go
func TestDecomposeEndpoint(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Too big", "priority": "high", "kind": "feature",
	})
	id := parent["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decompose", token,
		map[string]any{"into": []string{"Phase one", "Phase two"}})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	epic := got["epic"].(map[string]any)
	if epic["kind"] != "epic" || epic["id"] != id {
		t.Fatalf("epic = %v, want %s converted in place", epic, id)
	}
	children := got["children"].([]any)
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	for _, c := range children {
		if c.(map[string]any)["state"] != "draft" {
			t.Fatalf("child %v, want state draft", c)
		}
	}
}

func TestDecomposeEndpointRejectsEmptyList(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Too big", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+parent["id"].(string)+"/decompose", token,
		map[string]any{"into": []string{}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestDecomposeEndpointRejectsLeasedTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Too big", "priority": "high", "kind": "feature",
	})
	id := parent["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token,
		map[string]any{"worktree": "wt-1"}); rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decompose", token,
		map[string]any{"into": []string{"A"}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}
```

Check the claim endpoint's request shape in `internal/api/lifecycle_test.go` and match it in `TestDecomposeEndpointRejectsLeasedTask`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/api/ -run TestDecomposeEndpoint -v`
Expected: FAIL — 404, the route does not exist.

- [ ] **Step 3: Write the handler**

Create `internal/api/hierarchy.go`:

```go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

type decomposeRequest struct {
	Into []string `json:"into"`
}

// decomposeResponse returns both halves of the split: the converted epic,
// keeping its id, and the children it now tracks.
type decomposeResponse struct {
	Epic     taskJSON   `json:"epic"`
	Children []taskJSON `json:"children"`
}

// decomposeTask handles POST /api/v1/tasks/{id}/decompose: convert the task
// into an epic and create one draft child per title, in one transaction. This
// is the supported way out of the spec-005 needs_decomposition gate.
func (s *server) decomposeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req decomposeRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if len(req.Into) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "into must list at least one child title")
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actor := actorFrom(r)

	var children []store.Task
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.decomposed", payload,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			children, err = store.Decompose(tx, s.st.Now(), id, req.Into, actor.ID, eventID)
			return err
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	epic, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := decomposeResponse{Epic: toTaskJSON(epic), Children: make([]taskJSON, 0, len(children))}
	for i := range children {
		resp.Children = append(resp.Children, toTaskJSON(&children[i]))
	}
	writeJSON(w, http.StatusCreated, resp)
}
```

- [ ] **Step 4: Register the route**

In `internal/api/server.go`, after the `edges` routes:

```go
	mux.Handle("POST /api/v1/tasks/{id}/decompose", s.auth(s.decomposeTask))
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run TestDecomposeEndpoint -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/hierarchy.go internal/api/server.go internal/api/hierarchy_test.go
git commit -m "Add POST /api/v1/tasks/{id}/decompose"
```

---

### Task 10: CLI client — wire types and methods

**Files:**
- Modify: `internal/cli/client.go` (task wire types, `TaskListFilter`, `CreateTaskInput`, `Brief`, new methods)
- Test: `internal/cli/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cli/client_test.go` (match the file's existing fake-server helper; if it uses `httptest.NewServer` with a handler switch, follow that shape):

```go
func TestClientHierarchyCalls(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotMethod, gotBody = r.URL.RequestURI(), r.Method, string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"epic":{"id":"WL-1","kind":"epic"},"children":[{"id":"WL-2"}]}`))
	}))
	defer srv.Close()
	c := NewClient(Config{ServerURL: srv.URL, Token: "t"})

	if _, err := c.Parent(context.Background(), "WL-2", "WL-1"); err != nil {
		t.Fatalf("Parent: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-2/edges" {
		t.Fatalf("Parent hit %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"child_of"`) || !strings.Contains(gotBody, `"to":"WL-1"`) {
		t.Fatalf("Parent body = %s", gotBody)
	}

	if _, err := c.Unparent(context.Background(), "WL-2", "WL-1"); err != nil {
		t.Fatalf("Unparent: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("Unparent method = %s, want DELETE", gotMethod)
	}

	resp, _, err := c.Decompose(context.Background(), "WL-1", []string{"A"})
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if gotPath != "/api/v1/tasks/WL-1/decompose" {
		t.Fatalf("Decompose hit %s", gotPath)
	}
	if resp.Epic.Kind != "epic" || len(resp.Children) != 1 {
		t.Fatalf("Decompose response = %+v", resp)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run TestClientHierarchyCalls -v`
Expected: FAIL — `c.Parent`, `c.Unparent`, `c.Decompose` undefined.

- [ ] **Step 3: Add the wire types**

In `internal/cli/client.go`:

Add `Parent string \`json:"parent,omitempty"\`` to `CreateTaskInput`.

Add `Kind string` and `Parent string` to `TaskListFilter`, and send them in `ListTasks`:

```go
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Parent != "" {
		q.Set("parent", f.Parent)
	}
```

Add, next to `TaskDetail`:

```go
// TaskParent is the one-hop-up projection of a task's parent epic.
type TaskParent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// TaskProgress is an epic's derived roll-up: closed of total direct children.
type TaskProgress struct {
	Closed int `json:"closed"`
	Total  int `json:"total"`
}

// TaskHierarchy is the hierarchy block on a task detail: the parent (nil for a
// root task) and the derived child progress.
type TaskHierarchy struct {
	Parent   *TaskParent  `json:"parent"`
	Progress TaskProgress `json:"progress"`
}
```

and the field on `TaskDetail`:

```go
	Hierarchy TaskHierarchy `json:"hierarchy"`
```

Add `Parent *TaskParent \`json:"parent"\`` to `Brief`, documented as "the task's epic, one hop up; nil for a root task".

- [ ] **Step 4: Add the methods**

Next to `Block`/`Unblock`:

```go
// Parent calls POST /api/v1/tasks/{id}/edges to file id under an epic.
func (c *Client) Parent(ctx context.Context, id, epic string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &epic, Type: "child_of"})
}

// Unparent calls DELETE /api/v1/tasks/{id}/edges to detach id from its epic.
func (c *Client) Unparent(ctx context.Context, id, epic string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &epic, Type: "child_of"})
}

// DecomposeResponse is the wire form of POST /api/v1/tasks/{id}/decompose:
// the converted epic, keeping its id, and the children it now tracks.
type DecomposeResponse struct {
	Epic     Task   `json:"epic"`
	Children []Task `json:"children"`
}

// Decompose calls POST /api/v1/tasks/{id}/decompose.
func (c *Client) Decompose(ctx context.Context, id string, titles []string) (DecomposeResponse, []byte, error) {
	raw, err := c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/decompose",
		map[string]any{"into": titles})
	if err != nil {
		return DecomposeResponse{}, nil, err
	}
	var resp DecomposeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return DecomposeResponse{}, nil, fmt.Errorf("decode decompose response: %w", err)
	}
	return resp, raw, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run TestClientHierarchyCalls -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "Add hierarchy calls and wire types to the CLI client"
```

---

### Task 11: CLI commands — `add --parent`, `parent`, `unparent`, `tree`, `list --parent`, `decompose`

**Files:**
- Modify: `internal/cmd/task.go`
- Modify: `internal/cli/render.go` (`TaskDetailRender`, new `TreeRender`)
- Test: `internal/cmd/task_test.go`, `internal/cli/render_test.go`

- [ ] **Step 1: Write the failing render test**

Append to `internal/cli/render_test.go`:

```go
func TestTaskDetailRenderHierarchy(t *testing.T) {
	var buf bytes.Buffer
	TaskDetailRender(&buf, TaskDetail{
		Task: Task{ID: "WL-2", Title: "Piece", Project: "proj", Priority: "medium",
			Kind: "feature", State: "ready"},
		Hierarchy: TaskHierarchy{Parent: &TaskParent{ID: "WL-1", Title: "Container", State: "in_progress"}},
	})
	if got := buf.String(); !strings.Contains(got, "parent:   WL-1") {
		t.Fatalf("output has no parent line:\n%s", got)
	}

	buf.Reset()
	TaskDetailRender(&buf, TaskDetail{
		Task: Task{ID: "WL-1", Title: "Container", Project: "proj", Priority: "medium",
			Kind: "epic", State: "in_progress"},
		Hierarchy: TaskHierarchy{Progress: TaskProgress{Closed: 3, Total: 7}},
	})
	if got := buf.String(); !strings.Contains(got, "progress: 3/7") {
		t.Fatalf("output has no progress line:\n%s", got)
	}
}

func TestTreeRender(t *testing.T) {
	var buf bytes.Buffer
	TreeRender(&buf, []TreeNode{{
		Epic:     Task{ID: "WL-1", Title: "Container", State: "in_progress"},
		Progress: TaskProgress{Closed: 1, Total: 2},
		Children: []Task{
			{ID: "WL-2", Title: "Done piece", State: "merged"},
			{ID: "WL-3", Title: "Open piece", State: "ready"},
		},
	}})
	got := buf.String()
	for _, want := range []string{"WL-1", "Container", "1/2", "WL-2", "WL-3", "merged"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tree output missing %q:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cli/ -run 'TestTaskDetailRenderHierarchy|TestTreeRender' -v`
Expected: FAIL — no parent/progress lines, `TreeRender` and `TreeNode` undefined.

- [ ] **Step 3: Extend `TaskDetailRender` and add `TreeRender`**

In `internal/cli/render.go`, inside `TaskDetailRender`, after the `state:` line:

```go
	if t.Hierarchy.Parent != nil {
		fmt.Fprintf(w, "  parent:   %s  %s (%s)\n",
			t.Hierarchy.Parent.ID, t.Hierarchy.Parent.Title, t.Hierarchy.Parent.State)
	}
	if t.Hierarchy.Progress.Total > 0 {
		fmt.Fprintf(w, "  progress: %d/%d children closed\n",
			t.Hierarchy.Progress.Closed, t.Hierarchy.Progress.Total)
	}
```

and append:

```go
// TreeNode is one epic and its direct children, with the epic's derived
// progress — the unit `lode task tree` renders.
type TreeNode struct {
	Epic     Task
	Progress TaskProgress
	Children []Task
}

// TreeRender prints each epic with its progress, then its children indented
// one level. Depth is capped at two edges, so there is no deeper nesting to
// render.
func TreeRender(w io.Writer, nodes []TreeNode) {
	for i, n := range nodes {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s  [%s]  %d/%d closed\n",
			n.Epic.ID, n.Epic.Title, n.Epic.State, n.Progress.Closed, n.Progress.Total)
		for _, c := range n.Children {
			fmt.Fprintf(w, "  %s  %s  (%s)\n", c.ID, c.Title, c.State)
		}
		if len(n.Children) == 0 {
			fmt.Fprintln(w, "  (no children)")
		}
	}
	if len(nodes) == 0 {
		fmt.Fprintln(w, "no epics")
	}
}
```

- [ ] **Step 4: Run the render tests to verify they pass**

Run: `go test ./internal/cli/ -run 'TestTaskDetailRenderHierarchy|TestTreeRender' -v`
Expected: PASS

- [ ] **Step 5: Add the commands**

In `internal/cmd/task.go`, register the new subcommands in `newTaskCmd`, immediately after `newTaskUnblockCmd()`:

```go
		newTaskParentCmd(),
		newTaskUnparentCmd(),
		newTaskTreeCmd(),
		newTaskDecomposeCmd(),
```

Add `--parent` to `newTaskAddCmd`: declare `var parent string` with the other flag vars, pass `Parent: parent` in the `cli.CreateTaskInput`, and register

```go
	cmd.Flags().StringVar(&parent, "parent", "", "file the new task under this epic")
```

Add `--parent` to `newTaskListCmd`: declare `var parent string`, pass `Parent: parent` in the `cli.TaskListFilter`, and register

```go
	cmd.Flags().StringVar(&parent, "parent", "", "list only the direct children of this epic")
```

Then append the four new command constructors:

```go
func newTaskParentCmd() *cobra.Command {
	var under string
	cmd := &cobra.Command{
		Use:   "parent <id>",
		Short: "File a task under an epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.Parent(cmd.Context(), args[0], under)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now a child of %s\n", args[0], under)
			return nil
		},
	}
	cmd.Flags().StringVar(&under, "under", "", "id of the epic to file it under (required)")
	cmd.MarkFlagRequired("under")
	return cmd
}

func newTaskUnparentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unparent <id>",
		Short: "Detach a task from its epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			// The edge is identified by both endpoints, and the caller only
			// knows the child, so read the parent back first.
			t, _, err := c.GetTask(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if t.Hierarchy.Parent == nil {
				return fmt.Errorf("%s has no parent", args[0])
			}
			epic := t.Hierarchy.Parent.ID
			raw, err := c.Unparent(cmd.Context(), args[0], epic)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer a child of %s\n", args[0], epic)
			return nil
		},
	}
	return cmd
}

func newTaskTreeCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "tree [id]",
		Short: "Show epics and their children, with per-epic progress",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}

			var epics []cli.Task
			if len(args) == 1 {
				t, _, err := c.GetTask(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				epics = []cli.Task{t.Task}
			} else {
				resp, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
					Project: resolveProject(cmd, project, cfg.CurrentProject), Kind: "epic",
				})
				if err != nil {
					return err
				}
				epics = resp.Tasks
			}

			nodes := make([]cli.TreeNode, 0, len(epics))
			for _, e := range epics {
				detail, _, err := c.GetTask(cmd.Context(), e.ID)
				if err != nil {
					return err
				}
				kids, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{Parent: e.ID})
				if err != nil {
					return err
				}
				nodes = append(nodes, cli.TreeNode{
					Epic: e, Progress: detail.Hierarchy.Progress, Children: kids.Tasks,
				})
			}
			cli.TreeRender(cmd.OutOrStdout(), nodes)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project id"+projectFlagUsage)
	return cmd
}

func newTaskDecomposeCmd() *cobra.Command {
	var into []string
	cmd := &cobra.Command{
		Use:   "decompose <id>",
		Short: "Turn an oversized task into an epic plus its children, in place",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(into) == 0 {
				return fmt.Errorf("--into is required: pass one title per child")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.Decompose(cmd.Context(), args[0], into)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now an epic with %d children\n",
				resp.Epic.ID, len(resp.Children))
			cli.TaskTable(cmd.OutOrStdout(), resp.Children)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&into, "into", nil, "child title (repeatable; one per child)")
	cmd.MarkFlagRequired("into")
	return cmd
}
```

Note: `--into` is a repeatable `StringArrayVar`, so the spec's `--into "A" "B" "C"` form is written `--into "A" --into "B" --into "C"`. Say so in the flag usage string.

- [ ] **Step 6: Write the command test**

Append to `internal/cmd/task_test.go`. It uses the harness already in this package: `lifecycleTestServer`, `setupProject`, `createTestTask` (all in `internal/cmd/lifecycle_test.go`), `runLode`, and `taskListIDs`.

```go
func TestTaskHierarchyCommands(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	epic, _, err := c.CreateTask(context.Background(), cli.CreateTaskInput{
		Project: "proj", Title: "Container", Priority: "high", Kind: "epic",
	})
	if err != nil {
		t.Fatalf("create epic: %v", err)
	}

	// add --parent files the new task under the epic in one round trip.
	out, err := runLode(t, "task", "add", "--json", "--project", "proj",
		"--title", "Piece", "--parent", epic.ID)
	if err != nil {
		t.Fatalf("task add --parent: %v\noutput: %s", err, out)
	}
	var child cli.Task
	if err := json.Unmarshal([]byte(out), &child); err != nil {
		t.Fatalf("decode add output %q: %v", out, err)
	}

	if got := taskListIDs(t, "--parent", epic.ID); len(got) != 1 || got[0] != child.ID {
		t.Fatalf("list --parent = %v, want [%s]", got, child.ID)
	}

	show, err := runLode(t, "task", "show", child.ID)
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	if !strings.Contains(show, "parent:") || !strings.Contains(show, epic.ID) {
		t.Fatalf("show has no parent line:\n%s", show)
	}

	tree, err := runLode(t, "task", "tree", "--project", "proj")
	if err != nil {
		t.Fatalf("task tree: %v", err)
	}
	if !strings.Contains(tree, epic.ID) || !strings.Contains(tree, child.ID) {
		t.Fatalf("tree missing epic or child:\n%s", tree)
	}

	if _, err := runLode(t, "task", "unparent", child.ID); err != nil {
		t.Fatalf("task unparent: %v", err)
	}
	if got := taskListIDs(t, "--parent", epic.ID); len(got) != 0 {
		t.Fatalf("list --parent after unparent = %v, want []", got)
	}

	// decompose converts a task in place and creates its children as drafts.
	big := createTestTask(t, c, "Too big")
	out, err = runLode(t, "task", "decompose", big.ID, "--into", "A", "--into", "B")
	if err != nil {
		t.Fatalf("task decompose: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "is now an epic") {
		t.Fatalf("decompose output:\n%s", out)
	}
	if got := taskListIDs(t, "--parent", big.ID); len(got) != 2 {
		t.Fatalf("children of %s = %v, want 2", big.ID, got)
	}
}
```

- [ ] **Step 6b: Run it**

Run: `go test ./internal/cmd/ -run TestTaskHierarchyCommands -v`
Expected: PASS

- [ ] **Step 7: Verify the commands by hand**

Run: `go run ./cmd/lode task tree --help` and `go run ./cmd/lode task decompose --help`
Expected: both print usage with the documented flags.

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/task.go internal/cli/render.go internal/cli/render_test.go internal/cmd/task_test.go
git commit -m "Add lode task parent, unparent, tree, decompose, and --parent filters"
```

---

### Task 12: Board — group an epic's children under it

**Files:**
- Modify: `internal/api/admin.go:513-516` (`boardTaskJSON`), `:581-652` (`assembleBoard`)
- Modify: `internal/cli/client.go` (`BoardTask`)
- Modify: `internal/cli/render.go` (`boardSection`)
- Test: `internal/cli/render_test.go`, `internal/api/admin_test.go`

- [ ] **Step 1: Write the failing render test**

Append to `internal/cli/render_test.go`:

```go
// TestBoardSectionGroupsChildren checks that an epic's children render
// directly beneath it, in id order, whatever order the server sent them.
func TestBoardSectionGroupsChildren(t *testing.T) {
	var buf bytes.Buffer
	BoardRender(&buf, BoardResponse{Projects: []BoardProject{{
		ID: "proj", Name: "Proj",
		Ready: []BoardTask{
			{Task: Task{ID: "WL-9", Title: "Loose", Priority: "medium"}},
			{Task: Task{ID: "WL-3", Title: "Child B", Priority: "medium"}, Parent: "WL-1"},
			{Task: Task{ID: "WL-1", Title: "Container", Priority: "medium"}},
			{Task: Task{ID: "WL-2", Title: "Child A", Priority: "medium"}, Parent: "WL-1"},
		},
	}}})
	got := buf.String()
	epic := strings.Index(got, "WL-1")
	childA := strings.Index(got, "WL-2")
	childB := strings.Index(got, "WL-3")
	loose := strings.Index(got, "WL-9")
	if !(epic < childA && childA < childB) {
		t.Fatalf("children are not grouped under their epic:\n%s", got)
	}
	if loose < epic {
		t.Fatalf("the loose task should sort by its own id, after WL-1:\n%s", got)
	}
	if !strings.Contains(got, "└ WL-2") {
		t.Fatalf("child rows are not marked:\n%s", got)
	}
}
```

Check the exact names of the board wire types in `internal/cli/client.go` (`BoardResponse`, its project type, `BoardTask`) and adjust the literal to match.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/cli/ -run TestBoardSectionGroupsChildren -v`
Expected: FAIL — `BoardTask` has no `Parent` field.

- [ ] **Step 3: Carry the parent on board tasks**

In `internal/api/admin.go`:

```go
// boardTaskJSON is a board row. Parent is the task's epic when it has one, so
// a board can group an epic's children under it without a lookup per task.
type boardTaskJSON struct {
	taskJSON
	Parent string      `json:"parent,omitempty"`
	Holder *holderJSON `json:"holder,omitempty"`
}
```

In `assembleBoard`, after the `blocked` lookup:

```go
	parents, err := s.st.ParentMap(ctx, projectFilter)
	if err != nil {
		return nil, err
	}
```

and where each row is built:

```go
			bt := boardTaskJSON{taskJSON: toTaskJSON(t), Parent: parents[t.ID]}
```

In `internal/cli/client.go`, add the matching field to `BoardTask`:

```go
	Parent string `json:"parent,omitempty"`
```

- [ ] **Step 4: Group and mark the rows in `boardSection`**

In `internal/cli/render.go`:

```go
// boardGroupKey keeps an epic and its children adjacent within a bucket: a
// child sorts under its parent's id (rank 1), a parent or loose task under its
// own (rank 0).
func boardGroupKey(t BoardTask) (string, int) {
	if t.Parent != "" {
		return t.Parent, 1
	}
	return t.ID, 0
}

func boardSection(w io.Writer, label string, tasks []BoardTask) {
	if len(tasks) == 0 {
		return
	}
	rows := make([]BoardTask, len(tasks))
	copy(rows, tasks)
	sort.SliceStable(rows, func(i, j int) bool {
		ki, ri := boardGroupKey(rows[i])
		kj, rj := boardGroupKey(rows[j])
		if ki != kj {
			return ki < kj
		}
		if ri != rj {
			return ri < rj
		}
		return rows[i].ID < rows[j].ID
	})

	fmt.Fprintf(w, "\n%s\n", label)
	tw := newTabwriter(w)
	fmt.Fprintln(tw, "ID\tPRIORITY\tTITLE\tHOLDER")
	for _, t := range rows {
		holder := "-"
		if t.Holder != nil {
			holder = fmt.Sprintf("%s (until %s)", t.Holder.ActorID, localTime(t.Holder.ExpiresAt))
		}
		id := t.ID
		if t.Parent != "" {
			id = "└ " + id
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, t.Priority, t.Title, holder)
	}
	tw.Flush()
}
```

Add `"sort"` to the file's imports.

Note: `boardGroupKey` sorts ids as strings, so within one epic `WL-10` precedes `WL-9`. That is a cosmetic ordering wart inside a group, not a correctness issue, and it matches how ids already sort elsewhere in this renderer. Leave it.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -run TestBoardSection -v && go test ./internal/api/ -run TestBoard -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin.go internal/cli/client.go internal/cli/render.go internal/cli/render_test.go
git commit -m "Group an epic's children under it on the board"
```

---

### Task 13: Web — parent, children, and progress on the task page

**Files:**
- Modify: `internal/api/web.go:122-194`
- Modify: `internal/api/templates/task.html:22-23`
- Test: `internal/api/web_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/web_test.go`, matching the file's existing page-fetch helper:

```go
func TestTaskPageShowsProgress(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")
	for _, title := range []string{"A", "B"} {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
			"parent": epic,
		})
	}

	body := getPage(t, h, "/tasks/"+epic)
	if !strings.Contains(body, "0/2") {
		t.Fatalf("task page has no progress:\n%s", body)
	}
}
```

Use whatever this file already calls to fetch a page; if it inlines `httptest.NewRecorder`, do the same rather than adding a helper.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestTaskPageShowsProgress -v`
Expected: FAIL — no progress on the page.

- [ ] **Step 3: Populate progress in the handler**

In `internal/api/web.go`, add to `taskPageData`:

```go
	Progress  store.HierarchyProgress
```

and in `taskPage`, after the edge loops:

```go
	progress, err := s.st.ChildProgress(ctx, id)
	if err != nil {
		s.webStoreErr(w, err)
		return
	}
	data.Progress = progress
```

- [ ] **Step 4: Render it**

In `internal/api/templates/task.html`, replace the Children line:

```html
  <li>Children: {{if .Children}}{{range .Children}}<a href="/tasks/{{.}}">{{.}}</a> {{end}}<span class="muted">({{.Progress.Closed}}/{{.Progress.Total}} closed)</span>{{else}}<span class="muted">none</span>{{end}}</li>
```

Note: inside `{{range}}` the dot is rebound, so `.Progress` must sit outside the range block, as written above.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/api/ -run TestTaskPageShowsProgress -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/api/web.go internal/api/templates/task.html internal/api/web_test.go
git commit -m "Show epic progress on the web task page"
```

---

### Task 14: End-to-end coverage and spec sign-off

**Files:**
- Create: `e2e/hierarchy_test.go`
- Modify: `docs/specs/004-execution-backbone.md`
- Modify: `README.md` (task command list, if it enumerates subcommands)

- [ ] **Step 1: Write the e2e test**

Create `e2e/hierarchy_test.go`, following `e2e/pickup_test.go`'s shape (public surfaces only — `cli.Client` over `httptest`, no direct store writes):

```go
//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestHierarchyLoop exercises spec 018 end-to-end: decompose an oversized task
// into an epic plus children, confirm the epic never reaches the ready set,
// then close the children and watch the epic roll up on its own.
func TestHierarchyLoop(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "hier", Name: "Hier", Key: "HIER",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e hierarchy", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	big, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "hier", Title: "Ship the thing", Priority: "critical", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 1. Decompose: the id survives, the kind flips, children appear as drafts.
	split, _, err := agent.Decompose(ctx, big.ID, []string{"Phase one", "Phase two"})
	if err != nil {
		t.Fatalf("decompose: %v", err)
	}
	if split.Epic.ID != big.ID || split.Epic.Kind != "epic" {
		t.Fatalf("epic = %+v, want %s converted in place", split.Epic, big.ID)
	}
	if len(split.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(split.Children))
	}

	// 2. The epic is never handed out, even ranked critical.
	for _, c := range split.Children {
		if _, _, err := agent.ReadyTask(ctx, c.ID); err != nil {
			t.Fatalf("publish %s: %v", c.ID, err)
		}
	}
	pick, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{Project: "hier", DryRun: true})
	if err != nil {
		t.Fatalf("claim-next dry run: %v", err)
	}
	if pick.Task == nil || pick.Task.ID == big.ID {
		t.Fatalf("claim-next picked %+v, want a child, never the epic", pick.Task)
	}

	// 3. Closing every child rolls the epic up on its own. Abandon is the only
	//    close the HTTP API can drive unaided — in_progress -> in_review is the
	//    GitHub PR hook's move — and all-abandoned is the roll-up case most
	//    worth proving end to end: it must not report cancelled work as
	//    shipped. The merged path is covered by the store tests.
	for _, c := range split.Children {
		if _, _, err := agent.ClaimTask(ctx, c.ID, "wt-e2e-"+c.ID, 0); err != nil {
			t.Fatalf("claim %s: %v", c.ID, err)
		}
		if _, _, err := agent.AbandonTask(ctx, c.ID); err != nil {
			t.Fatalf("abandon %s: %v", c.ID, err)
		}
	}

	epic, _, err := agent.GetTask(ctx, big.ID)
	if err != nil {
		t.Fatalf("get epic: %v", err)
	}
	if epic.State != "abandoned" {
		t.Fatalf("epic state = %s, want abandoned", epic.State)
	}
	if epic.Hierarchy.Progress.Closed != 2 || epic.Hierarchy.Progress.Total != 2 {
		t.Fatalf("progress = %+v, want 2/2", epic.Hierarchy.Progress)
	}
}
```

Each child claims a distinct worktree identity: an active lease is unique per worktree, so reusing one string would make the second claim lose the race.

- [ ] **Step 2: Run the e2e suite**

Run: `go test -tags e2e ./e2e/... -run TestHierarchyLoop -v`
Expected: PASS

- [ ] **Step 3: Record the resolved questions in the spec**

In `docs/specs/004-execution-backbone.md`, replace the `## Open questions` section with:

```markdown
## Resolved

- **Q018.1 — Does an epic need wrap-up work?** No. Closure is automatic, and a
  final integration or documentation step is a child task rather than a reason
  to make closure manual. Revisit if real usage contradicts it.
- **Q018.2 — Should `lode task done` on an epic be an error or a manual
  override?** An error. `done` is `in_review -> merged` and `in_review` is
  forbidden for epics, so the kind guard in `Transition` rejects it with a
  message naming the roll-up rule. There is no override.
```

- [ ] **Step 4: Record the two implied rules the implementation adopted**

In the same file, add to the **Decisions** table:

| Decision | Choice |
|---|---|
| Parent kind | Must already be `kind = 'epic'`; `AddEdge` rejects any other parent (422) |
| Direct claim of an epic | Rejected in `Claim` as well as excluded from the ready set |

and a short paragraph under it:

```markdown
**Why the parent must already be an epic:** it is what makes "declared" real.
`ready -> in_progress` is a legal epic transition — it is the roll-up trigger —
so excluding epics from the ready set alone would still let `lode task claim
<epic-id>` through; `Claim` carries the same guard. Two supported ways to get
an epic: create one (`lode task add --kind epic`) or convert in place (`lode
task decompose`). There is no `lode task edit --kind`.
```

- [ ] **Step 5: Update the CLI surface in the README if it lists subcommands**

Run: `grep -n "task block\|task brief" README.md`
If the README enumerates `lode task` subcommands, add `parent`, `unparent`, `tree`, and `decompose` in the same style. If it does not, skip this step.

- [ ] **Step 6: Full verification**

Run:
```bash
go build ./... && go vet ./... && go test ./internal/... && go test -tags e2e ./e2e/...
```
Expected: all four succeed. Paste the output before claiming completion.

- [ ] **Step 7: Commit**

```bash
git add e2e/hierarchy_test.go docs/specs/004-execution-backbone.md README.md
git commit -m "Cover the hierarchy loop end-to-end and record the resolved spec questions"
```

---

## Spec coverage check

| Spec section | Task |
|---|---|
| Migration `0006_task_hierarchy` (kind, single-parent index, children index) | 1 |
| Depth cap, single parent, cross-project rejection, `AddEdge` returns walk length | 2 |
| Progress derived on read | 3 |
| Never claimable (`readyCandidates` + `Claim`) | 4 |
| Restricted state machine, `ResolveDelivery` early return | 4 |
| Roll-up resolver and all five edge cases, hooked into `Transition` | 5 |
| Roll-up attribution to the child's `event_id` | 5 |
| Depth-2 recursion in one transaction | 5 |
| `Brief.Parent`, one hop | 6 |
| `decompose` semantics and lease rejection | 7 |
| API: `parent` on create, `hierarchy` on detail, edge validation status codes | 8 |
| API: `POST /tasks/{id}/decompose` | 9 |
| CLI: `add --parent`, `parent`, `unparent`, `tree`, `list --parent`, `decompose`, `show` lines | 10, 11 |
| `lode board` groups children under their epic | 12 |
| Web task page | 13 |
| Testing section (every bullet) | 1–14 |
| Out of scope: ordering, blocker inheritance, cross-project epics, estimates, graph projection | not implemented, by design |
