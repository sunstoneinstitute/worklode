---
status: accepted
task: WL-26
covers:
  - spec: docs/specs/006-knowledge-graph.md#sec-11
    coverage: partial
  - spec: docs/specs/006-knowledge-graph.md#sec-16
    coverage: partial
---
# Knowledge graph 2/2 (spec 006): the backbone→graph projector — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 2. Part 1
(`2026-07-30-knowledge-graph-1-graph-foundations.md`, executed by WL-25,
merged) built the graph side: `internal/kg/iri`, the `wl:priority`/`wl:concern`
declarations, `internal/graphproj` (row→triples→deterministic Turtle) and the
Oxigraph test harness. This part wires it to the backbone. Task numbers restart
at 1 (`docs/authoring-design-docs.md`).

**Rewritten 2026-08-19 (WL-110).** The version accepted 2026-07-31 (`7a61bc6`)
became unexecutable: it targeted `internal/graph` — a SPARQL-Update client that
was never built, against an endpoint graph-server does not have — and the
Workstream graphs that `70d2139` retired (025 acceptance criterion 20). Part
1's rewrite (WL-108, `52f5960`) settled the replacements this plan now builds
on: the write unit is the **whole project graph**, rendered by
`internal/graphproj` and `PUT` by `internal/graphserver`; membership is
`wl:inProject` in `iri.ProjectGraph(<project-id>)`; the mapping consumes
`internal/model` types (ADR 036). The corpus fold (`e9065b5`) also renumbered
the acceptance criteria the old plan cited — "criteria 4 and 10b" are gone; the
projector criterion in the folded spec is §16 criterion 1.

**Goal:** Every task mutation becomes observable in the `state_log` outbox, a
checkpointed projector loop under `lode serve` re-renders each dirty project's
graph from the backbone and replaces it in graph-server, and the full slice —
lifecycle event → idempotent projection → SPARQL read-back including
`wl:dependsOn+` reachability — is proven against Oxigraph.

**Architecture:** `internal/projector` polls `state_log` for dirty **projects**
(a task event dirties its project's graph), re-renders each dirty project —
Project node plus every task row, via part 1's `graphproj.ProjectTriples` /
`TaskTriples` / `Document` — and replaces that project's named graph wholesale
with `graphserver.Client.PutGraph` on the fixed `main` branch (006 §11 as
amended by part 1; graph-server has no SPARQL Update endpoint). The watermark
lives in a new single-row `graph_projection` table. `lode serve` runs the loop
as a background goroutine when `LODE_GRAPHSERVER_URL` is set.

**Tech Stack:** Go 1.26, PostgreSQL via `database/sql`, standard-library
testing, `internal/graphserver` (GSP over `net/http`), Oxigraph (docker) as the
test endpoint via `internal/graphproj/graphtest`.

**Spec:** `docs/specs/006-knowledge-graph.md` — read it via
`docs/specs/inlined/006-knowledge-graph.md`.

---

## Already built — do not recreate

- **The mapping and rendering.** `internal/graphproj`: `TaskTriples` /
  `ProjectTriples` (model types in, subject-complete triples out) and
  `Document` (deterministic Turtle — the idempotence lever: an unchanged
  re-render is byte-identical). Do not re-derive the predicate table; part 1's
  Task 4 fixed it.
- **The client.** `internal/graphserver`: branch-scoped GSP
  `PutGraph`/`GetGraph`/`DeleteGraph`, SPARQL `Select`, Keycloak
  client-credentials via `FromEnv` (`LODE_GRAPHSERVER_URL` + the three
  `LODE_GRAPHSERVER_*` auth vars). Proven against the real graph-server by
  `e2e/graphserver_test.go`. This plan gives it its first production caller.
- **The test harness.** `internal/graphproj/graphtest` (test-only Oxigraph
  loader: `Endpoint`, `PutGraph`, `Select`), the compose `oxigraph` service,
  and the CI wiring (`TEST_SPARQL_URL`). The old plan's own `graphtest`
  package was never built and must not be.
- **Most of the outbox.** `state_log` rows are already written, with an
  `eventID`, by `Transition`, assignment (`internal/store/assign.go`), field
  edits (`internal/api/tasks.go` PATCH), secrets, and decompose. The gap is
  exactly `CreateTask`, `AddEdge`, `RemoveEdge` (Task 1).

## Design calls

1. **The write unit is the whole project graph.** graph-server exposes no
   SPARQL Update (`internal/graphserver/client.go` package doc), so the old
   plan's per-subject `DELETE`/`INSERT` cannot run against the system of
   record. The projector recomputes every task of a dirty project from the
   backbone — which stays the source of truth; there is no read-modify-write
   of graph state — and `PutGraph`s the full graph to the fixed `main` branch
   (006 §13.2 item 5). Deterministic rendering makes an unchanged
   re-projection byte-identical, so a re-PUT after a crash or duplicated
   batch is a no-op replace.
2. **Dirty tracking is per project, over `state_log`.** `DirtyProjects` scans
   `state_log` past the watermark and joins `tasks.project_id`: any task
   event dirties its project; an edge change dirties both endpoints' projects
   because Task 1 logs both endpoints (edges may cross projects —
   `internal/store/tasks.go` allows it by design, and each side's triples
   live in its own project graph).
3. **Not an eventbus subscriber.** `internal/eventbus` (spec 025 §15) now
   exists and looks like the natural consumer, but it is the wrong tool here:
   its handler is strictly per-event, so a burst of edits would trigger one
   graph render per event with no way to coalesce a batch into one PUT per
   dirty project; and the `events` payloads are heterogeneous — `task.created`
   carries the request body, which has no task id — so mapping event→project
   would mean parsing every payload shape. `state_log` is the entity-grained
   outbox (`entity_kind`, `entity_id`, `event_id` FK for provenance), which is
   exactly the dirtiness signal. The projector keeps its own watermark
   (`graph_projection`) over `state_log` ids. The single-consumer lock the
   eventbus offers is not needed while one `lode serve` replica runs the loop
   — the same standing assumption the lease sweeper makes, and the reason 006
   §13.3 keeps If-Match CAS a should-have (tracked in `docs/follow-ups.md`).
4. **Every task row projects, in every state.** Draft and abandoned tasks are
   projected with their state literal — the graph answers "what was
   abandoned"; filtering would silently fork the backbone's row set. A task
   leaves the graph only when its row leaves the backbone, and no delete path
   exists today; if one ever does, the whole-graph render drops the task
   automatically on the project's next projection.
5. **`wl:produces` is scoped out, with the spec row flagged.** 006 §11's v1
   table projects `wl:produces` (Task→Deliverable) and `wl:affects`
   (Task→Component), but the backbone stores neither relation: deliverables
   are rows (spec 029) with no task edge, components have no backbone
   representation at all. Part 1's mapping deliberately omits them (emitting
   a predicate without a source is fabrication), so the projector cannot
   write them, and adding a task→deliverable edge is 029-adjacent schema and
   surface design that this plan does not smuggle in. The spec-vs-backbone
   decision is filed as WL-116; the §11 rows stay unsatisfiable until their
   backbone sources exist. `wl:mirrors` and
   `wl:requiresSkill` are out for the same reason (part 1, "Deliberately not
   in this part").
6. **Project-node staleness is accepted for v1.** The dirty scan sees task
   events only, so a project rename re-projects on that project's next task
   event, not immediately (project edits write no `state_log` row today).
   Project names change rarely and nothing consumes the Project node's
   `dct:title` graph-side yet; the fix, whenever a consumer appears, is a
   `LogChange` on the project-edit paths plus widening the dirty scan.
7. **Coverage is declared per section of the folded spec.** §11 stays
   `partial` even with part 1's claim: the projector half of the
   task-projection mechanism lands here, but the `produces`/`affects`/
   `mirrors` rows (design call 5) and §11.1's runtime projection (the
   runtime-layer plan, WL-27) remain. §16 is `partial`: criterion 1's
   projector path (authenticate → `PUT` → SPARQL read-back) becomes real
   worklode code proven against Oxigraph here and against dev graph-server by
   `e2e/graphserver_test.go`, while prod remains blocked on §13.2 item 1 and
   the other nine criteria belong to the vocabulary/runtime plans.

## Deliberately not in this plan

- **`wl:produces`, `wl:affects`, `wl:mirrors`, `wl:requiresSkill`** — design
  call 5.
- **Runtime projection (006 §11.1)** — the runtime-layer plan (WL-27, after
  WL-111's sweep).
- **A compose graph-server / e2e projector journey.** The projector's e2e
  proof against real graph-server needs the compose graph-server service
  tracked in `docs/follow-ups.md` ("Compose gets its own graph-server");
  until it exists, Oxigraph (Task 5) plus `e2e/graphserver_test.go` cover the
  two halves separately.
- **If-Match CAS on writes** — 006 §13.3 item 6, tracked in
  `docs/follow-ups.md`; single-writer for v1.
- **Backfill/replay tooling.** A full re-projection is `UPDATE
  graph_projection SET last_state_log_id = 0` (or any watermark rewind) — the
  loop re-renders every project with `state_log` history; document it, build
  nothing.

---

## File structure

**New files**

| Path | Responsibility |
|---|---|
| `deploy/base/migrations/NNNN_graph_projection.up.sql` | watermark table + `state_log (entity_kind, id)` index |
| `deploy/base/migrations/NNNN_graph_projection.down.sql` | drop both |
| `internal/store/outbox_test.go` | create/edge mutations write `state_log` rows |
| `internal/store/projection.go` | checkpoint get/set + `DirtyProjects` over `state_log` |
| `internal/store/projection_test.go` | checkpoint round-trip; dirty scan, dedupe, limit, cross-project edge |
| `internal/projector/projector.go` | render dirty projects → `PutGraph`, advance checkpoint |
| `internal/projector/projector_test.go` | real store + fake graph-server; idempotence; two-project edge fan-out; error retry |
| `internal/projector/metrics.go` | spec 022 instruments (nil-safe) |
| `internal/projector/metrics_test.go` | counters move as RunOnce runs |
| `internal/projector/oxigraph_test.go` | full slice vs. Oxigraph incl. `wl:dependsOn+` and abandoned-task projection |

`NNNN` is the next free migration number at execution time (0026 as of this
rewrite; `./scripts/check-migrations.sh` renumbers on collision). List both
files in `deploy/base/kustomization.yaml`.

**Modified files**

| Path | Change |
|---|---|
| `internal/store/tasks.go` | `CreateTask`/`AddEdge`/`RemoveEdge` gain `eventID` and write `state_log` rows |
| `internal/store/inbox.go` | `PromoteIssue` gains `eventID`, passes it to `CreateTask` |
| `internal/store/hierarchy.go` | decompose's `CreateTask`/`AddEdge` calls pass the `eventID` already in scope |
| `internal/api/tasks.go` | create (+ parent/follow-up edges), `addEdge`, `removeEdge` pass `eventID` |
| `internal/api/webform.go` | web create passes `eventID` |
| `internal/api/admin.go` | promote (+ parent edge) passes `eventID` |
| `internal/cmd/serve.go` | `graphProjector` helper + background loop next to the sweeper |
| `internal/cmd/serve_test.go` | env gating: unset → disabled, set → projector, broken → boot error |
| `README.md` | "Knowledge graph projection" section |

**Test commands**

- Postgres-backed:
  `docker compose up -d postgres && go test -trimpath ./internal/store/... ./internal/api/... ./internal/projector/...`
- Full slice: `docker compose up -d postgres oxigraph && go test -trimpath ./internal/projector/...`
- Everything: `make test`

**Global constraints** (once, per the repo's standing rules): the new
background loop and outbound call carry `worklode_*` metrics with tests and
bounded labels — never a project or task id as a label value (Task 4); the
migration is a new numbered pair listed in `deploy/base/kustomization.yaml`,
never an edit to a shipped one; store tests need Postgres (a skipped test
proved nothing); every task leaves `make test` green.

---

## Tasks

### Task 1 — Complete the task outbox

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Task transitions, assignments and field edits already write `state_log` rows;
creates and edge changes do not, so a `state_log`-driven projector would miss
them. Close the gap at the store layer so the invariant is "every task
mutation writes its own outbox row".

**Files:**
- Modify: `internal/store/tasks.go` (`CreateTask`, `AddEdge`, `RemoveEdge` gain `eventID`)
- Modify: `internal/store/inbox.go` (`PromoteIssue` gains `eventID`), `internal/store/hierarchy.go` (decompose call sites)
- Modify: `internal/api/tasks.go`, `internal/api/webform.go`, `internal/api/admin.go` (pass `eventID`)
- Test: `internal/store/outbox_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// outboxStore returns a migrated store with two projects.
func outboxStore(t *testing.T) *Store {
	t.Helper()
	s := OpenTestStore(t)
	for _, p := range [][3]string{{"alpha", "Alpha", "AL"}, {"beta", "Beta", "BE"}} {
		if err := s.CreateProject(t.Context(), p[0], p[1], p[2]); err != nil {
			t.Fatalf("create project %s: %v", p[0], err)
		}
	}
	return s
}

// makeTask creates a ready task through the event log, as the API does.
func makeTask(t *testing.T, s *Store, extID, project, title string) *model.Task {
	t.Helper()
	var created *model.Task
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := CreateTask(tx, time.Now().UTC(), TaskInput{
				ProjectID: project, Title: title, Priority: "medium", Kind: "feature",
			}, eventID)
			if err != nil {
				return err
			}
			created = task
			return nil
		})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return created
}

func TestCreateTaskWritesStateLog(t *testing.T) {
	s := outboxStore(t)
	task := makeTask(t, s, "e1", "alpha", "outbox on create")

	entries, err := s.StateLogForEntity(t.Context(), "task", task.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("state_log entries after create = %d; want 1", len(entries))
	}
}

func TestEdgeChangesWriteStateLogForBothTasks(t *testing.T) {
	s := outboxStore(t)
	a := makeTask(t, s, "e2", "alpha", "blocker")
	b := makeTask(t, s, "e3", "alpha", "blocked")

	_, _, err := s.RecordEvent(t.Context(), "cli", "e4", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, time.Now().UTC(), a.ID, b.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}
	for _, id := range []string{a.ID, b.ID} {
		entries, err := s.StateLogForEntity(t.Context(), "task", id)
		if err != nil {
			t.Fatalf("state log %s: %v", id, err)
		}
		if len(entries) != 2 { // create + edge add
			t.Fatalf("%s entries = %d; want 2 (create + edge)", id, len(entries))
		}
	}

	_, _, err = s.RecordEvent(t.Context(), "cli", "e5", "task.edge_removed", nil,
		func(tx *sql.Tx, eventID int64) error {
			return RemoveEdge(tx, a.ID, b.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("remove edge: %v", err)
	}
	entries, err := s.StateLogForEntity(t.Context(), "task", b.ID)
	if err != nil {
		t.Fatalf("state log: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries after removal = %d; want 3", len(entries))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -trimpath ./internal/store/ -run 'TestCreateTaskWritesStateLog|TestEdgeChanges'`
Expected: FAIL — compile error: `CreateTask`/`AddEdge`/`RemoveEdge` do not take an `eventID`

- [ ] **Step 3: Extend the store functions**

In `internal/store/tasks.go`:

1. `CreateTask` becomes
   `func CreateTask(tx *sql.Tx, now time.Time, in TaskInput, eventID int64) (*model.Task, error)`
   and, immediately after the successful `INSERT INTO tasks`, appends the
   row (edit style — no `old` on a create):

```go
	if err := LogChange(tx, "task", id, eventID,
		map[string]string{"field": "state", "new": state}); err != nil {
		return nil, err
	}
```

2. `AddEdge` becomes
   `func AddEdge(tx *sql.Tx, now time.Time, fromTask, toTask, typ string, eventID int64) error`
   and, after the successful edge insert (and cycle check), logs **both
   endpoints** — this is what lets the projector dirty both projects of a
   cross-project edge:

```go
	change := map[string]string{"field": "edge", "op": "add", "type": typ,
		"from": fromTask, "to": toTask}
	if err := LogChange(tx, "task", fromTask, eventID, change); err != nil {
		return err
	}
	if err := LogChange(tx, "task", toTask, eventID, change); err != nil {
		return err
	}
```

3. `RemoveEdge` becomes
   `func RemoveEdge(tx *sql.Tx, fromTask, toTask, typ string, eventID int64) error`
   with the same two `LogChange` calls (`"op": "remove"`) after the delete.

Update the three doc comments to mention the appended `state_log` row
(mirror `Transition`'s comment style). In `internal/store/inbox.go`,
`PromoteIssue` gains a trailing `eventID int64` parameter and passes it to
`CreateTask`. In `internal/store/hierarchy.go`, decompose's `CreateTask` and
`AddEdge` calls pass the `eventID` already in scope.

- [ ] **Step 4: Update the API call sites**

All of them already sit inside a `RecordEvent` apply callback, so the id is
at hand:

- `internal/api/tasks.go` create: `store.CreateTask(tx, now, store.TaskInput{…}, eventID)`,
  and the two conditional `store.AddEdge(tx, now, t.ID, …, eventID)` calls
  (parent, follow-up) below it
- `internal/api/tasks.go` `addEdge`/`removeEdge`: change the callback
  parameter `_ int64` to `eventID int64` and pass it through
- `internal/api/webform.go` create: pass `eventID`
- `internal/api/admin.go` promote: `store.PromoteIssue(tx, …, eventID)` and
  the parent `store.AddEdge(…, eventID)`

- [ ] **Step 5: Fix remaining callers and run everything**

Run: `go build ./... && go test -trimpath ./internal/store/... ./internal/api/...`
Expected: test files that call the old signatures fail to compile; every
call site sits inside a `RecordEvent` apply callback — pass that callback's
`eventID` through. Then: PASS. Tests asserting exact `state_log` contents
may now see one extra "created" entry; update their expectations, do not
weaken the new invariant.

- [ ] **Step 6: Commit**

```bash
git add internal/store internal/api
git commit -m "Write a state_log row for every task mutation"
```

---

### Task 2 — The projection checkpoint and dirty-project scan

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `deploy/base/migrations/NNNN_graph_projection.up.sql` / `.down.sql` (next free `NNNN`; list both in `deploy/base/kustomization.yaml`)
- Create: `internal/store/projection.go`
- Test: `internal/store/projection_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestProjectionCheckpointRoundTrip(t *testing.T) {
	s := OpenTestStore(t)
	ctx := t.Context()

	cp, err := s.ProjectionCheckpoint(ctx)
	if err != nil || cp != 0 {
		t.Fatalf("initial checkpoint = %d, %v; want 0, nil", cp, err)
	}
	if err := s.SetProjectionCheckpoint(ctx, 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	if cp, err = s.ProjectionCheckpoint(ctx); err != nil || cp != 42 {
		t.Fatalf("checkpoint = %d, %v; want 42, nil", cp, err)
	}
}

func TestDirtyProjects(t *testing.T) {
	s := outboxStore(t) // from outbox_test.go: projects alpha and beta
	ctx := t.Context()
	a := makeTask(t, s, "d1", "alpha", "first")
	makeTask(t, s, "d2", "beta", "second")

	projects, through, err := s.DirtyProjects(ctx, 0, 100)
	if err != nil {
		t.Fatalf("dirty: %v", err)
	}
	if len(projects) != 2 || projects[0] != "alpha" || projects[1] != "beta" {
		t.Fatalf("projects = %v; want [alpha beta] in first-touched order", projects)
	}
	if through == 0 {
		t.Fatal("through = 0; must advance past the scanned rows")
	}

	// Nothing new after the watermark.
	projects, again, err := s.DirtyProjects(ctx, through, 100)
	if err != nil || len(projects) != 0 || again != through {
		t.Fatalf("after watermark: projects=%v through=%d err=%v; want none, unchanged", projects, again, err)
	}

	// Repeat changes to one project dedupe to one entry.
	for i, move := range [][2]string{{"ready", "in_progress"}, {"in_progress", "ready"}} {
		_, _, err = s.RecordEvent(ctx, "cli", "d-move-"+move[1], "task.transition", nil,
			func(tx *sql.Tx, eventID int64) error {
				return Transition(tx, time.Now().UTC(), a.ID, move[0], move[1], eventID)
			})
		if err != nil {
			t.Fatalf("transition %d: %v", i, err)
		}
	}
	projects, _, err = s.DirtyProjects(ctx, through, 100)
	if err != nil || len(projects) != 1 || projects[0] != "alpha" {
		t.Fatalf("dirty after transitions = %v, %v; want just [alpha]", projects, err)
	}

	// A cross-project edge dirties both projects (Task 1 logs both endpoints).
	c := makeTask(t, s, "d3", "beta", "cross blocker")
	_, edgeFrom, err := s.DirtyProjects(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("watermark before edge: %v", err)
	}
	_, _, err = s.RecordEvent(ctx, "cli", "d-edge", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, time.Now().UTC(), c.ID, a.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}
	projects, _, err = s.DirtyProjects(ctx, edgeFrom, 100)
	if err != nil || len(projects) != 2 {
		t.Fatalf("dirty after cross-project edge = %v, %v; want both projects", projects, err)
	}

	// The limit bounds the scan, and through only covers what was read.
	projects, part, err := s.DirtyProjects(ctx, 0, 1)
	if err != nil || len(projects) != 1 || projects[0] != "alpha" {
		t.Fatalf("limited scan = %v, %v; want just [alpha]", projects, err)
	}
	if part >= through {
		t.Fatalf("limited through = %d; want < %d (only one row covered)", part, through)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -trimpath ./internal/store/ -run 'TestProjectionCheckpoint|TestDirtyProjects'`
Expected: FAIL — `undefined: (*Store).ProjectionCheckpoint`

- [ ] **Step 3: Write the migration**

`NNNN_graph_projection.up.sql`:

```sql
-- Watermark for the backbone→knowledge-graph projector (spec 006 §11): one
-- row; last_state_log_id is the state_log id through which task changes have
-- been projected. The index serves DirtyProjects' (entity_kind, id > $1)
-- scan, so non-task rows are never touched.
CREATE TABLE graph_projection (
    id                integer PRIMARY KEY CHECK (id = 1),
    last_state_log_id bigint  NOT NULL DEFAULT 0
);
INSERT INTO graph_projection (id, last_state_log_id) VALUES (1, 0);

CREATE INDEX state_log_kind_id ON state_log (entity_kind, id);
```

`NNNN_graph_projection.down.sql`:

```sql
DROP INDEX state_log_kind_id;
DROP TABLE graph_projection;
```

Add both filenames to `deploy/base/kustomization.yaml`.

- [ ] **Step 4: Write the store methods**

`internal/store/projection.go`: `ProjectionCheckpoint(ctx) (int64, error)`
and `SetProjectionCheckpoint(ctx, id) error` over the single row, and:

```go
// DirtyProjects returns the distinct project ids whose tasks have state_log
// activity after the given watermark, in first-touched order, and the last
// state_log id the scan covered (== after when there was nothing). limit
// bounds the number of log rows read, so one projection batch is bounded
// even after a long outage.
//
// The LEFT JOIN keeps the watermark advancing even over a log row whose
// task no longer resolves (no delete path exists today; this is a guard,
// not a feature). state_log ids are assigned at insert time, so a slow
// transaction can in principle commit a lower id after a higher one was
// already projected; with a single API server and short transactions the
// window is negligible for v1, and a watermark rewind re-renders everything.
func (s *Store) DirtyProjects(ctx context.Context, after int64, limit int) (projects []string, through int64, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sl.id, t.project_id
		   FROM state_log sl LEFT JOIN tasks t ON t.id = sl.entity_id
		  WHERE sl.entity_kind = 'task' AND sl.id > $1
		  ORDER BY sl.id LIMIT $2`,
		after, limit)
	// … scan; through = last id seen; skip NULL project_ids; dedupe with a
	// seen map preserving first-touched order.
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -trimpath ./internal/store/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations deploy/base/kustomization.yaml \
        internal/store/projection.go internal/store/projection_test.go
git commit -m "Track the graph-projection watermark over state_log"
```

---

### Task 3 — The projector

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

**Files:**
- Create: `internal/projector/projector.go`
- Test: `internal/projector/projector_test.go`

The fake graph-server is an `httptest.Server` implementing the one surface
the projector uses: `PUT /branches/main/graphs?graph=<iri>` (201 on first
sight of a graph, 204 after, 500 when told to fail), recording bodies per
graph IRI. The real client (`graphserver.New(srv.URL, nil)`) talks to it, so
the tests exercise the production wire path.

- [ ] **Step 1: Write the failing test**

```go
package projector_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graphserver"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeGraphServer records PUT bodies per graph IRI and can be told to fail.
type fakeGraphServer struct {
	mu   sync.Mutex
	fail bool
	puts map[string][]string // graph IRI → bodies, in arrival order
}

func (f *fakeGraphServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/branches/main/graphs" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		g := r.URL.Query().Get("graph")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if f.puts == nil {
			f.puts = map[string][]string{}
		}
		status := http.StatusNoContent
		if len(f.puts[g]) == 0 {
			status = http.StatusCreated
		}
		f.puts[g] = append(f.puts[g], string(body))
		w.WriteHeader(status)
	})
}

func (f *fakeGraphServer) last(graph string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b := f.puts[graph]; len(b) > 0 {
		return b[len(b)-1]
	}
	return ""
}

func (f *fakeGraphServer) count(graph string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts[graph])
}

func newProjector(t *testing.T) (*store.Store, *projector.Projector, *fakeGraphServer) {
	t.Helper()
	s := store.OpenTestStore(t)
	for _, p := range [][3]string{{"alpha", "Alpha", "AL"}, {"beta", "Beta", "BE"}} {
		if err := s.CreateProject(t.Context(), p[0], p[1], p[2]); err != nil {
			t.Fatalf("create project %s: %v", p[0], err)
		}
	}
	f := &fakeGraphServer{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return s, projector.New(s, graphserver.New(srv.URL, nil), nil, 100), f
}

// createTask creates a ready task through the outbox, as the API does.
func createTask(t *testing.T, s *store.Store, extID, project, title string) string {
	t.Helper()
	var id string
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, time.Now().UTC(), store.TaskInput{
				ProjectID: project, Title: title, Priority: "medium", Kind: "feature",
			}, eventID)
			if err != nil {
				return err
			}
			id = task.ID
			return nil
		})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return id
}

func TestRunOnceProjectsCreatedTask(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	id := createTask(t, s, "p1", "alpha", "wire the projector")

	n, err := p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1 project, nil", n, err)
	}
	doc := f.last(iri.ProjectGraph("alpha"))
	for _, want := range []string{
		"<" + iri.Task(id) + "> <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> <" + iri.Term("Task") + ">",
		"<" + iri.Task(id) + "> <" + iri.Term("taskState") + "> \"ready\"",
		"<" + iri.Task(id) + "> <" + iri.Term("taskKind") + "> <" + iri.Concept("feature") + ">",
		"<" + iri.Task(id) + "> <" + iri.Term("inProject") + "> <" + iri.Project("alpha") + ">",
		"<" + iri.Project("alpha") + "> <http://purl.org/dc/terms/title> \"Alpha\"",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("project graph missing %q\n%s", want, doc)
		}
	}

	// Checkpoint advanced: a second run is a no-op with no new PUT.
	if n, err := p.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d, %v; want 0, nil", n, err)
	}
	if got := f.count(iri.ProjectGraph("alpha")); got != 1 {
		t.Fatalf("PUTs after idempotent rerun = %d; want 1", got)
	}
}

func TestCrossProjectEdgeProjectsBothGraphs(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	a := createTask(t, s, "p2", "alpha", "blocker")
	b := createTask(t, s, "p3", "beta", "blocked")
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("drain creates: %v", err)
	}

	_, _, err := s.RecordEvent(ctx, "cli", "p4", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, time.Now().UTC(), a, b, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}

	n, err := p.RunOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RunOnce after edge = %d, %v; want both projects", n, err)
	}
	if doc := f.last(iri.ProjectGraph("alpha")); !strings.Contains(doc,
		"<"+iri.Task(a)+"> <"+iri.Term("blocks")+"> <"+iri.Task(b)+">") {
		t.Errorf("alpha graph missing wl:blocks\n%s", doc)
	}
	if doc := f.last(iri.ProjectGraph("beta")); !strings.Contains(doc,
		"<"+iri.Task(b)+"> <"+iri.Term("dependsOn")+"> <"+iri.Task(a)+">") {
		t.Errorf("beta graph missing wl:dependsOn\n%s", doc)
	}
}

func TestRunOnceLeavesCheckpointOnError(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	createTask(t, s, "p5", "alpha", "unlucky")

	f.fail = true
	if _, err := p.RunOnce(ctx); err == nil {
		t.Fatal("RunOnce against a failing endpoint returned nil error")
	}
	if cp, err := s.ProjectionCheckpoint(ctx); err != nil || cp != 0 {
		t.Fatalf("checkpoint after failure = %d, %v; must stay 0 for the retry", cp, err)
	}

	f.fail = false
	if n, err := p.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("retry RunOnce = %d, %v; want 1, nil", n, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -trimpath ./internal/projector/...`
Expected: FAIL — `no required module provides package .../internal/projector`

- [ ] **Step 3: Write the implementation**

`internal/projector/projector.go` — package doc cites 006 §11 (authority
stays split; Task is the bridge, projected read-only; design facts are never
projected) and the whole-project-graph mechanism:

```go
// Branch is the fixed graph-server branch the work graph lives on
// (spec 006 §13.2 item 5).
const Branch = "main"

// Projector re-renders dirty projects from the backbone and replaces their
// named graphs — idempotent per project graph: deterministic rendering
// (graphproj.Document) makes an unchanged re-projection byte-identical, so
// re-running after a crash or duplicated batch is safe.
type Projector struct {
	st    *store.Store
	gc    *graphserver.Client
	m     *Metrics // nil-safe; Task 4
	batch int
}

// New returns a projector reading at most batch state_log rows per run.
func New(st *store.Store, gc *graphserver.Client, m *Metrics, batch int) *Projector

// RunOnce projects every project dirtied since the checkpoint, then
// advances the checkpoint. It returns how many project graphs were
// (re-)written. On error the checkpoint is left untouched so the next run
// retries the same batch.
func (p *Projector) RunOnce(ctx context.Context) (int, error)
```

`RunOnce`: `ProjectionCheckpoint` → `DirtyProjects(cp, batch)` → for each
project id, render and `PutGraph(ctx, Branch, iri.ProjectGraph(id), doc)` →
`SetProjectionCheckpoint(through)`. Rendering one project: `GetProject`
(skip on `store.ErrNotFound` — a guard, no delete path exists),
`ListTasks(store.TaskFilter{Project: id})` (no state filter — design call
4), `ListEdgesForTasks(ids)`, then `graphproj.ProjectTriples` on a
`model.Project{ID, Name}` built from the store row plus `graphproj.TaskTriples`
per task with the `store.Edge` slices converted to `model.Edge`
(`FromTask`→`From`, `ToTask`→`To`), all through `graphproj.Document`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -trimpath ./internal/projector/...`
Expected: PASS (skipped without Postgres)

- [ ] **Step 5: Commit**

```bash
git add internal/projector
git commit -m "Project dirty projects into their named graphs"
```

---

### Task 4 — Projector metrics

```yaml
kind: feature
priority: high
blockedBy: [3]
```

Spec 022: the projector is a background loop making outbound calls, so it
carries instruments in its owning package — nil-safe struct in
`internal/projector/metrics.go`, `prometheus.Registerer` threaded from
`serve.go` (Task 6), bounded label values, `worklode_` prefix. Follow
`internal/eventbus/metrics.go` as the pattern.

**Files:**
- Create: `internal/projector/metrics.go`
- Test: `internal/projector/metrics_test.go`

- [ ] **Step 1: Write the failing test** — using
  `prometheus.NewRegistry()` + `testutil.ToFloat64`: a successful `RunOnce`
  over one dirty project increments `runs_total{result="ok"}` and
  `projects_total` by 1; a failing endpoint increments
  `runs_total{result="error"}` and leaves `projects_total` unchanged; a
  `nil` `*Metrics` records nothing and panics nowhere (the Task 3 tests
  already pass nil — they must stay green)
- [ ] **Step 2: Run** `go test -trimpath ./internal/projector/ -run TestMetrics` — FAIL
- [ ] **Step 3: Implement** — `Metrics` with
  `worklode_graph_projection_runs_total{result}` (CounterVec, both series
  pre-initialised so alert expressions see 0),
  `worklode_graph_projection_projects_total` (Counter: project graphs
  written), and `worklode_graph_projection_duration_seconds` (Histogram,
  buckets `{0.01, 0.1, 1, 10}`, one observation per run);
  `NewMetrics(reg prometheus.Registerer) *Metrics`; nil-receiver-safe
  recording methods called from `RunOnce`. No project or task id ever
  becomes a label value
- [ ] **Step 4: Run the package tests** — PASS
- [ ] **Step 5: Commit**

---

### Task 5 — The full slice against Oxigraph

```yaml
kind: feature
priority: high
blockedBy: [3]
```

Proves the projector half of §16 criterion 1 (lifecycle event → idempotent
projection → SPARQL read-back) plus the §3 transitive-property promise
(`wl:dependsOn+`, query-time, no reasoner) and design call 4 (abandoned
tasks stay projected). Oxigraph stands in as the conformant store; the real
graph-server wire path is covered by Task 3's fake and by
`e2e/graphserver_test.go`. The bridge is a small translating proxy: an
`httptest.Server` that accepts the client's
`PUT /branches/main/graphs?graph=<iri>` and forwards method, body and
`Content-Type` to Oxigraph's `PUT /store?graph=<iri>`, copying the status
back — so the projector runs unmodified against `graphserver.New(proxy.URL, nil)`.

**Files:**
- Test: `internal/projector/oxigraph_test.go` (create)

- [ ] **Step 1: Write the test** — `TestProjectorEndToEnd`:
  1. `graphtest.Endpoint(t)` (skips without Oxigraph), the translating
     proxy, `store.OpenTestStore(t)`, and a **unique project id**
     (`"kg-" + hex` — the graph IRI derives from it, isolating the run in a
     shared Oxigraph; register a `t.Cleanup` that DELETEs
     `/store?graph=<iri.ProjectGraph(proj)>`)
  2. Seed tasks `a`, `b`, `c` and edges `a blocks b`, `b blocks c` through
     `RecordEvent` as in Task 3's helpers; `RunOnce` → 1 project graph
  3. Read back with `graphtest.Select` against `iri.ProjectGraph(proj)`:
     `a`'s `wl:taskState` binds exactly one solution, `"ready"`; the
     property path `<c> <wl:dependsOn>+ <a>` binds (criterion 1's read-back
     + §3 transitivity); a `GROUP BY`/`COUNT` query shows exactly one
     `wl:inProject` per projected task (025 acceptance criterion 20's shape)
  4. Transition `a` `ready → in_progress`, `RunOnce` → 1; re-query: exactly
     one state literal and it is `"in_progress"` (whole-graph replace leaves
     no stale literal behind)
  5. Transition `c` `ready → abandoned`, `RunOnce` → 1; re-query: `c` is
     still present with `wl:taskState "abandoned"` (design call 4)
- [ ] **Step 2: Run it** —
  `docker compose up -d postgres oxigraph && go test -trimpath ./internal/projector/ -run TestProjectorEndToEnd -v`
  — PASS (skips if either service is down and the CI/TEST_SPARQL_URL pair is
  unset, per `graphtest.Endpoint`'s contract)
- [ ] **Step 3: Run the whole tree** — `make test` — PASS
- [ ] **Step 4: Commit**

---

### Task 6 — Run the projector under `lode serve`, document it

```yaml
kind: feature
priority: high
blockedBy: [3, 4]
```

**Files:**
- Modify: `internal/cmd/serve.go` (env gating + background loop, next to the lease sweeper)
- Test: `internal/cmd/serve_test.go` (add)
- Modify: `README.md`

- [ ] **Step 1: Write the failing test** — in `internal/cmd/serve_test.go`,
  `TestGraphProjector`: with `t.Setenv("LODE_GRAPHSERVER_URL", "")` the
  helper returns `(nil, nil)` (projection disabled); with
  `t.Setenv("LODE_GRAPHSERVER_URL", "http://localhost:9999")` it returns a
  non-nil projector; with the URL set and
  `LODE_GRAPHSERVER_TOKEN_URL` set but the client id/secret empty it
  returns an error (a half-configured endpoint fails the boot rather than
  silently disabling — `graphserver.FromEnv`'s contract). Pass
  `prometheus.NewRegistry()` and a nil store; construction touches neither
- [ ] **Step 2: Run** `go test -trimpath ./internal/cmd/ -run TestGraphProjector` —
  FAIL — `undefined: graphProjector`
- [ ] **Step 3: Write the wiring** — in `internal/cmd/serve.go`:

```go
// graphProjector builds the knowledge-graph projector when
// LODE_GRAPHSERVER_URL is set (spec 006 §11): the same LODE_GRAPHSERVER_*
// variables graphserver.FromEnv documents, so serve and every other caller
// share one configuration surface. Unset means projection is disabled;
// set-but-broken fails the boot.
func graphProjector(reg prometheus.Registerer, st *store.Store) (*projector.Projector, error) {
	if os.Getenv("LODE_GRAPHSERVER_URL") == "" {
		return nil, nil
	}
	gc, err := graphserver.FromEnv()
	if err != nil {
		return nil, err
	}
	return projector.New(st, gc, projector.NewMetrics(reg), 200), nil
}
```

  Then in the serve `RunE`, directly after the sweeper goroutine: build it
  (returning the error fails the boot) and, when non-nil, start a goroutine
  in the sweeper's exact shape — 10 s `time.Ticker`, `ctx.Done()` exit,
  `context.Canceled` treated as shutdown, `slog.Error("graph projection", …)`
  on failure and `slog.Info("projected project graphs", "count", n)` when
  `n > 0`. A comment cites 006 §11 and the single-projector assumption
  (§13.3 item 6: per-branch lock now, If-Match CAS before any second writer)
- [ ] **Step 4: Run the tests** — `go build ./... && go test -trimpath ./internal/cmd/...` — PASS
- [ ] **Step 5: Document it** — add a "Knowledge graph projection" section to
  `README.md` after the existing server-configuration material: when
  `LODE_GRAPHSERVER_URL` is set, `lode serve` mirrors every project's tasks
  into the knowledge graph (spec 006) — a background projector follows the
  `state_log` outbox and replaces each dirty project's named graph
  (`https://worklode.io/ns/graph/project/<id>`) on graph-server's `main`
  branch, checkpointed in `graph_projection`; name the four
  `LODE_GRAPHSERVER_*` variables (base URL required; the three Keycloak
  client-credentials variables together or not at all); note that a
  watermark rewind (`UPDATE graph_projection SET last_state_log_id = 0`)
  forces a full re-projection, and that the compose stack has no
  graph-server (Oxigraph is test-only; see `docs/follow-ups.md`)
- [ ] **Step 6: Run everything once more** —
  `docker compose up -d postgres oxigraph && make test` — PASS
- [ ] **Step 7: Commit**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go README.md
git commit -m "Run the graph projector under lode serve"
```
