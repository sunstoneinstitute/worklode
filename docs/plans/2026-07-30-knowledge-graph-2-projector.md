# Knowledge graph 2/2 (spec 006): the backbone→graph projector — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 2. Task numbering is global across the series: this plan
holds Tasks 7–11; `2026-07-30-knowledge-graph-1-graph-foundations.md` (Tasks
1–6) must be merged first. See part 1's header for the series-wide context —
the phase-1 scope of spec 006, what is deferred to later plans, the amendments
honored, and the full file-structure tables.

**Goal:** Wire the graph layer built in part 1 to the backbone: complete the
`state_log` outbox so every task mutation is observable, checkpoint the
projection, and run a projector loop under `lode serve` that mirrors each dirty
task into its Workstream named graph. Ends with backbone tasks mirrored into
the graph in production.

**Architecture:** `internal/projector` polls the existing `state_log` outbox
for dirty task IDs, re-renders each task with part 1's `internal/graph` and
pushes a per-subject-replace SPARQL update, advancing a checkpoint stored in a
new `graph_projection` table. `lode serve` starts the loop as a background
goroutine when `LODE_GRAPH_URL` is set.

**Tech Stack:** Go 1.26, cobra CLI, PostgreSQL via `database/sql`,
standard-library testing, SPARQL 1.1 Protocol over `net/http`, Oxigraph
(docker) as the test endpoint.

**Spec:** `docs/specs/006-knowledge-graph.md` (acceptance criteria 4 and 10b).

**Prerequisites (landed by part 1):** `internal/kg/iri` (the IRI grammar,
including `WorkstreamGraph`), `internal/graph` (`Triple`, `InsertData` /
`ReplaceSubject` rendering, `TaskTriples`, the SPARQL `Client`, and the
`graphtest` Oxigraph harness), the `rdf/wl/*.ttl` vocabulary sources, and the
compose `oxigraph` service plus its CI wiring.

Design calls this plan inherits (recorded in part 1, restated because they
shape Tasks 9–11):

- **Projected literal predicates.** `wl:taskState` (functional), `wl:priority`
  (functional) and `wl:concern` are projection-only mirrors of backbone enums;
  the projector writes them and the graph never forks them (Open Q3).
- **Workstream IRIs.** `id/workstream/<project-id>` for the instance,
  `https://worklode.io/ns/graph/workstream/<project-id>` for its projection
  named graph — the graph the projector replaces into.
- **One workstream per task in v1.** The backbone gives a task exactly one
  `project_id` and no way to move it, so the projector writes each task to
  exactly one Workstream graph. Multi-workstream membership (acceptance
  criterion 8) is real in the vocabulary and proven at the graph layer in part
  1's Task 6; the projector grows multi-graph fan-out when the backbone grows
  multi-workstream membership.

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `deploy/base/migrations/0008_graph_projection.up.sql` | `graph_projection` checkpoint table |
| `deploy/base/migrations/0008_graph_projection.down.sql` | drop it |
| `internal/store/projection.go` | checkpoint get/set + `DirtyTaskIDs` over `state_log` |
| `internal/store/projection_test.go` | checkpoint round-trip, dirty scan, dedupe, limit |
| `internal/store/outbox_test.go` | create/edge mutations write `state_log` rows |
| `internal/projector/projector.go` | the poll loop: dirty tasks → per-subject replace → advance checkpoint |
| `internal/projector/projector_test.go` | real store + fake SPARQL endpoint; idempotence; edge fan-out; error retry |
| `internal/projector/e2e_oxigraph_test.go` | full slice vs. Oxigraph incl. `wl:dependsOn+` property path (criterion 10b) |

Migration id `0008` is provisional: ids are assigned sequentially at execution
time by the migration-id script, with `0008` the current next-free (0001–0005
on main; 0006/0007 claimed by in-flight worktrees).

**Modified files**

| Path | Change |
|---|---|
| `internal/store/tasks.go:96-148` | `CreateTask` gains `eventID` and writes a `state_log` row |
| `internal/store/tasks.go:402-506` | `AddEdge`/`RemoveEdge` gain `eventID` and log both endpoints |
| `internal/api/tasks.go:112-129` | pass `eventID` to `CreateTask` |
| `internal/api/tasks.go:385-388, 424-427` | pass `eventID` to `AddEdge`/`RemoveEdge` |
| `internal/cmd/serve.go` | `graphProjectorFromEnv` + background projection loop |
| `README.md` | "Knowledge graph projection" section (env vars, compose service) |

**Test commands**

- Postgres-backed (skip if unreachable outside CI):
  `docker compose up -d postgres && go test ./internal/store/... ./internal/api/... ./internal/projector/...`
- Everything: `docker compose up -d postgres oxigraph && go test ./...`

---

## Task 7: Complete the task outbox

Task state transitions already write `state_log` rows
(`internal/store/tasks.go:154-179`); creates and edge changes do not, so a
`state_log`-driven projector would miss them. Close the gap at the store
layer so the invariant is "every task mutation writes its own outbox row".

**Files:**
- Modify: `internal/store/tasks.go` (`CreateTask`, `AddEdge`, `RemoveEdge` gain `eventID`)
- Modify: `internal/api/tasks.go:112-129` (create), `:385-388` (add edge), `:424-427` (remove edge)
- Test: `internal/store/outbox_test.go` (create)

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"database/sql"
	"testing"
	"time"
)

// outboxStore returns a migrated store with one project.
func outboxStore(t *testing.T) *Store {
	t.Helper()
	s := OpenTestStore(t)
	if err := s.CreateProject(t.Context(), "worklode", "Worklode", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return s
}

// makeTask creates a ready task through the event log, as the API does.
func makeTask(t *testing.T, s *Store, extID, title string) *Task {
	t.Helper()
	var created *Task
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := CreateTask(tx, time.Now().UTC(), TaskInput{
				ProjectID: "worklode", Title: title, Priority: "medium", Kind: "feature",
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
	task := makeTask(t, s, "e1", "outbox on create")

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
	a := makeTask(t, s, "e2", "blocker")
	b := makeTask(t, s, "e3", "blocked")

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

Run: `go test ./internal/store/ -run 'TestCreateTaskWritesStateLog|TestEdgeChanges'`
Expected: FAIL — compile error: `CreateTask`/`AddEdge`/`RemoveEdge` do not take an `eventID`

- [ ] **Step 3: Extend the store functions**

In `internal/store/tasks.go`:

1. Change the `CreateTask` signature to
   `func CreateTask(tx *sql.Tx, now time.Time, in TaskInput, eventID int64) (*Task, error)`
   and, immediately after the successful `INSERT INTO tasks`, add:

```go
	if err := LogChange(tx, "task", id, eventID,
		map[string]string{"field": "state", "old": "", "new": state}); err != nil {
		return nil, err
	}
```

2. Change `AddEdge` to
   `func AddEdge(tx *sql.Tx, now time.Time, fromTask, toTask, typ string, eventID int64) error`
   and, after the successful edge insert (and cycle check), add:

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

3. Change `RemoveEdge` to
   `func RemoveEdge(tx *sql.Tx, fromTask, toTask, typ string, eventID int64) error`
   with the same two `LogChange` calls (`"op": "remove"`) after the delete.

Update the doc comments of all three to mention the appended `state_log` row
(mirror `Transition`'s comment style at `internal/store/tasks.go:149-153`).

- [ ] **Step 4: Update the API call sites**

In `internal/api/tasks.go`:
- create (line ~114): `store.CreateTask(tx, s.st.Now(), store.TaskInput{…}, eventID)`
- `addEdge` (line ~386): change the apply callback parameter `_ int64` to
  `eventID int64` and call `store.AddEdge(tx, s.st.Now(), from, to, req.Type, eventID)`
- `removeEdge` (line ~425): likewise,
  `store.RemoveEdge(tx, from, to, req.Type, eventID)`

- [ ] **Step 5: Fix remaining callers and run everything**

Run: `go build ./... && go test ./internal/store/... ./internal/api/...`
Expected: a handful of test files fail to compile (e.g.
`internal/store/tasks_test.go`, `internal/store/projects_test.go`). Every
call site already sits inside a `RecordEvent` apply callback — pass that
callback's `eventID` through. Then: PASS. Tests that assert exact
`state_log` contents for a task may now see one extra "created" entry;
update their expectations, do not weaken the new invariant.

- [ ] **Step 6: Commit**

```bash
git add internal/store internal/api
git commit -m "Write a state_log row for every task mutation"
```

---

## Task 8: The projection checkpoint

**Files:**
- Create: `deploy/base/migrations/0008_graph_projection.up.sql`
- Create: `deploy/base/migrations/0008_graph_projection.down.sql`
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

func TestDirtyTaskIDs(t *testing.T) {
	s := outboxStore(t) // from outbox_test.go
	ctx := t.Context()
	a := makeTask(t, s, "d1", "first")
	b := makeTask(t, s, "d2", "second")

	ids, through, err := s.DirtyTaskIDs(ctx, 0, 100)
	if err != nil {
		t.Fatalf("dirty: %v", err)
	}
	if len(ids) != 2 || ids[0] != a.ID || ids[1] != b.ID {
		t.Fatalf("ids = %v; want [%s %s] in first-touched order", ids, a.ID, b.ID)
	}
	if through == 0 {
		t.Fatal("through = 0; must advance past the scanned rows")
	}

	// Nothing new after the watermark.
	ids, again, err := s.DirtyTaskIDs(ctx, through, 100)
	if err != nil || len(ids) != 0 || again != through {
		t.Fatalf("after watermark: ids=%v through=%d err=%v; want none, unchanged", ids, again, err)
	}

	// A transition dirties only its task, and repeat changes dedupe.
	for i, move := range [][2]string{{"ready", "in_progress"}, {"in_progress", "ready"}} {
		_, _, err = s.RecordEvent(ctx, "cli", "d-move-"+move[1], "task.transition", nil,
			func(tx *sql.Tx, eventID int64) error {
				return Transition(tx, time.Now().UTC(), b.ID, move[0], move[1], eventID)
			})
		if err != nil {
			t.Fatalf("transition %d: %v", i, err)
		}
	}
	ids, _, err = s.DirtyTaskIDs(ctx, through, 100)
	if err != nil || len(ids) != 1 || ids[0] != b.ID {
		t.Fatalf("dirty after transitions = %v, %v; want just [%s]", ids, err, b.ID)
	}

	// The limit bounds the scan, and through only covers what was read.
	ids, part, err := s.DirtyTaskIDs(ctx, 0, 1)
	if err != nil || len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("limited scan = %v, %v; want just [%s]", ids, err, a.ID)
	}
	if part >= through {
		t.Fatalf("limited through = %d; want < %d (only one row covered)", part, through)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run 'TestProjectionCheckpoint|TestDirtyTaskIDs'`
Expected: FAIL — `undefined: (*Store).ProjectionCheckpoint`

- [ ] **Step 3: Write the migration**

`deploy/base/migrations/0008_graph_projection.up.sql`:

```sql
-- Watermark for the backbone→knowledge-graph projector (spec 006
-- §Projection). One row; last_state_log_id is the state_log id through
-- which tasks have been projected. Numbered 0008 because 0006/0007 are
-- claimed by in-flight branches (task hierarchy, org-wide skills);
-- golang-migrate accepts gaps.
CREATE TABLE graph_projection (
    id                integer PRIMARY KEY CHECK (id = 1),
    last_state_log_id bigint  NOT NULL DEFAULT 0
);
INSERT INTO graph_projection (id, last_state_log_id) VALUES (1, 0);
```

`deploy/base/migrations/0008_graph_projection.down.sql`:

```sql
DROP TABLE graph_projection;
```

- [ ] **Step 4: Write the store methods**

`internal/store/projection.go`:

```go
package store

import (
	"context"
	"fmt"
)

// ProjectionCheckpoint returns the state_log id up to which tasks have been
// projected into the knowledge graph (spec 006 §Projection).
func (s *Store) ProjectionCheckpoint(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_state_log_id FROM graph_projection WHERE id = 1`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("projection checkpoint: %w", err)
	}
	return id, nil
}

// SetProjectionCheckpoint advances the projection watermark.
func (s *Store) SetProjectionCheckpoint(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE graph_projection SET last_state_log_id = $1 WHERE id = 1`, id); err != nil {
		return fmt.Errorf("set projection checkpoint: %w", err)
	}
	return nil
}

// DirtyTaskIDs returns the distinct task ids with state_log activity after
// the given watermark, in first-touched order, and the last state_log id
// the scan covered (== after when there was nothing). limit bounds the
// number of log rows read, so one projection batch is bounded even after a
// long outage.
//
// state_log ids are assigned at insert time, so a slow transaction can in
// principle commit a lower id after a higher one was already projected.
// With a single API server and short ingest transactions the window is
// negligible for v1; lode reconcile (spec 013) is the backstop.
func (s *Store) DirtyTaskIDs(ctx context.Context, after int64, limit int) (ids []string, through int64, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, entity_id FROM state_log
		 WHERE entity_kind = 'task' AND id > $1 ORDER BY id LIMIT $2`,
		after, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("dirty task ids: %w", err)
	}
	defer rows.Close()

	through = after
	seen := map[string]bool{}
	for rows.Next() {
		var logID int64
		var taskID string
		if err := rows.Scan(&logID, &taskID); err != nil {
			return nil, 0, fmt.Errorf("scan state log: %w", err)
		}
		through = logID
		if !seen[taskID] {
			seen[taskID] = true
			ids = append(ids, taskID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("dirty task ids: %w", err)
	}
	return ids, through, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/store/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0008_graph_projection.up.sql \
        deploy/base/migrations/0008_graph_projection.down.sql \
        internal/store/projection.go internal/store/projection_test.go
git commit -m "Track the graph-projection watermark over state_log"
```

---

## Task 9: The projector

**Files:**
- Create: `internal/projector/projector.go`
- Test: `internal/projector/projector_test.go`

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

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// fakeSPARQL records /update bodies and can be told to fail.
type fakeSPARQL struct {
	mu      sync.Mutex
	fail    bool
	updates []string
}

func (f *fakeSPARQL) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/update" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		f.updates = append(f.updates, string(body))
		w.WriteHeader(http.StatusNoContent)
	})
}

func (f *fakeSPARQL) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.updates, "\n---\n")
}

func newProjector(t *testing.T) (*store.Store, *projector.Projector, *fakeSPARQL) {
	t.Helper()
	s := store.OpenTestStore(t)
	if err := s.CreateProject(t.Context(), "worklode", "Worklode", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	f := &fakeSPARQL{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return s, projector.New(s, graph.NewClient(srv.URL, nil), 100), f
}

// createTask creates a ready task through the outbox, as the API does.
func createTask(t *testing.T, s *store.Store, extID, title string) *store.Task {
	t.Helper()
	var created *store.Task
	_, _, err := s.RecordEvent(t.Context(), "cli", extID, "task.created", nil,
		func(tx *sql.Tx, eventID int64) error {
			task, err := store.CreateTask(tx, time.Now().UTC(), store.TaskInput{
				ProjectID: "worklode", Title: title, Priority: "medium", Kind: "feature",
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

func TestRunOnceProjectsCreatedTask(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	task := createTask(t, s, "p1", "wire the projector")

	n, err := p.RunOnce(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1, nil", n, err)
	}
	u := f.all()
	for _, want := range []string{
		"DELETE WHERE { GRAPH <" + iri.WorkstreamGraph("worklode") + "> { <" +
			iri.Task(task.ID) + "> ?p ?o } }",
		"<" + iri.Task(task.ID) + "> <" + graph.RDFType + "> <" + iri.Term("Task") + ">",
		`"ready"`,
		"<" + iri.Concept("feature") + ">",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("update missing %q\n%s", want, u)
		}
	}

	// Checkpoint advanced: a second run is a no-op.
	if n, err := p.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d, %v; want 0, nil", n, err)
	}
	if got := len(strings.Split(f.all(), "\n---\n")); got != 1 {
		t.Fatalf("updates after idempotent rerun = %d; want 1", got)
	}
}

func TestRunOnceProjectsBothEdgeEndpoints(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	a := createTask(t, s, "p2", "blocker")
	b := createTask(t, s, "p3", "blocked")
	if _, err := p.RunOnce(ctx); err != nil {
		t.Fatalf("drain creates: %v", err)
	}

	_, _, err := s.RecordEvent(ctx, "cli", "p4", "task.edge_added", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.AddEdge(tx, time.Now().UTC(), a.ID, b.ID, "blocks", eventID)
		})
	if err != nil {
		t.Fatalf("add edge: %v", err)
	}

	n, err := p.RunOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RunOnce after edge = %d, %v; want both endpoints", n, err)
	}
	u := f.all()
	if !strings.Contains(u, "<"+iri.Task(a.ID)+"> <"+iri.Term("blocks")+"> <"+iri.Task(b.ID)+">") {
		t.Errorf("missing %s wl:blocks %s\n%s", a.ID, b.ID, u)
	}
	if !strings.Contains(u, "<"+iri.Task(b.ID)+"> <"+iri.Term("dependsOn")+"> <"+iri.Task(a.ID)+">") {
		t.Errorf("missing %s wl:dependsOn %s\n%s", b.ID, a.ID, u)
	}
}

func TestRunOnceLeavesCheckpointOnError(t *testing.T) {
	s, p, f := newProjector(t)
	ctx := t.Context()
	createTask(t, s, "p5", "unlucky")

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

Run: `go test ./internal/projector/...`
Expected: FAIL — `no required module provides package .../internal/projector`

- [ ] **Step 3: Write the implementation**

```go
// Package projector mirrors backbone execution facts into the knowledge
// graph (spec 006 §Projection). Authority stays split (D2/D3): the backbone
// owns execution facts and Task is the bridge (D11), projected read-only;
// design facts are authored graph-side and never projected.
package projector

import (
	"context"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// Projector consumes the state_log outbox and maintains each dirty task by
// a per-subject replace in its Workstream named graph - idempotent per
// (task, graph), so re-running after a crash or a duplicated batch is safe.
type Projector struct {
	st    *store.Store
	gc    *graph.Client
	batch int
}

// New returns a projector reading at most batch state_log rows per run.
func New(st *store.Store, gc *graph.Client, batch int) *Projector {
	return &Projector{st: st, gc: gc, batch: batch}
}

// RunOnce projects every task dirtied since the checkpoint, then advances
// the checkpoint. It returns how many tasks were (re-)projected. On error
// the checkpoint is left untouched so the next run retries the same batch.
func (p *Projector) RunOnce(ctx context.Context) (int, error) {
	cp, err := p.st.ProjectionCheckpoint(ctx)
	if err != nil {
		return 0, err
	}
	ids, through, err := p.st.DirtyTaskIDs(ctx, cp, p.batch)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	for i, id := range ids {
		if err := p.projectTask(ctx, id); err != nil {
			return i, fmt.Errorf("project %s: %w", id, err)
		}
	}
	if err := p.st.SetProjectionCheckpoint(ctx, through); err != nil {
		return len(ids), err
	}
	return len(ids), nil
}

// projectTask replaces the task's triples in its Workstream graph with a
// fresh projection of the current row - never the event delta - which is
// what makes replay safe: any event only marks the task dirty.
func (p *Projector) projectTask(ctx context.Context, id string) error {
	t, err := p.st.GetTask(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil // tasks are never deleted; guard anyway
	}
	if err != nil {
		return err
	}
	out, in, err := p.st.ListEdges(ctx, id)
	if err != nil {
		return err
	}
	update := graph.ReplaceSubject(
		iri.WorkstreamGraph(t.ProjectID), iri.Task(t.ID),
		graph.TaskTriples(*t, out, in))
	return p.gc.Update(ctx, update)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/projector/...`
Expected: PASS (3 tests; skipped without Postgres)

- [ ] **Step 5: Commit**

```bash
git add internal/projector
git commit -m "Project dirty tasks into their Workstream named graphs"
```

---

## Task 10: The full slice against Oxigraph

Proves acceptance criterion 4 (backbone lifecycle event → idempotent
projection → SPARQL read-back) and criterion 10b (`wl:dependsOn+`
reachability with no reasoner).

**Files:**
- Test: `internal/projector/e2e_oxigraph_test.go` (create)

- [ ] **Step 1: Write the test**

```go
package projector_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/graph/graphtest"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/projector"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestProjectorEndToEnd(t *testing.T) {
	base := graphtest.Endpoint(t)
	s := store.OpenTestStore(t)
	ctx := t.Context()

	// Unique project id: the Workstream graph IRI derives from it, which
	// isolates this run in the shared Oxigraph instance.
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random project id: %v", err)
	}
	proj := "kg-" + hex.EncodeToString(buf)
	if err := s.CreateProject(ctx, proj, "KG e2e", "KG"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	mk := func(ext, title string) *store.Task {
		t.Helper()
		var created *store.Task
		_, _, err := s.RecordEvent(ctx, "cli", ext, "task.created", nil,
			func(tx *sql.Tx, eventID int64) error {
				task, err := store.CreateTask(tx, time.Now().UTC(), store.TaskInput{
					ProjectID: proj, Title: title, Priority: "medium", Kind: "feature",
				}, eventID)
				if err != nil {
					return err
				}
				created = task
				return nil
			})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return created
	}
	a, b, c := mk("g1", "foundation"), mk("g2", "middle"), mk("g3", "tip")
	for i, e := range []struct{ from, to string }{{a.ID, b.ID}, {b.ID, c.ID}} {
		_, _, err := s.RecordEvent(ctx, "cli", fmt.Sprintf("g-edge-%d", i), "task.edge_added", nil,
			func(tx *sql.Tx, eventID int64) error {
				return store.AddEdge(tx, time.Now().UTC(), e.from, e.to, "blocks", eventID)
			})
		if err != nil {
			t.Fatalf("edge %d: %v", i, err)
		}
	}

	p := projector.New(s, graph.NewClient(base, nil), 100)
	if n, err := p.RunOnce(ctx); err != nil || n != 3 {
		t.Fatalf("RunOnce = %d, %v; want 3 projected tasks", n, err)
	}

	gc := graph.NewClient(base, nil)
	g := iri.WorkstreamGraph(proj)

	state := func(id string) []map[string]string {
		t.Helper()
		rows, err := gc.Select(ctx, fmt.Sprintf(
			"SELECT ?s WHERE { GRAPH <%s> { <%s> <%s> ?s } }",
			g, iri.Task(id), iri.Term("taskState")))
		if err != nil {
			t.Fatalf("state query %s: %v", id, err)
		}
		return rows
	}
	if rows := state(a.ID); len(rows) != 1 || rows[0]["s"] != "ready" {
		t.Fatalf("projected state of %s = %v; want ready", a.ID, rows)
	}

	// Criterion 10b: transitive reachability as a property path, no
	// reasoner. c dependsOn b dependsOn a, so dependsOn+ joins c to a.
	ok, err := gc.Ask(ctx, fmt.Sprintf(
		"ASK { GRAPH <%s> { <%s> <%s>+ <%s> } }",
		g, iri.Task(c.ID), iri.Term("dependsOn"), iri.Task(a.ID)))
	if err != nil || !ok {
		t.Fatalf("dependsOn+ reachability = %v, %v; want true", ok, err)
	}

	// A lifecycle event re-projects: exactly one state literal, the new one.
	_, _, err = s.RecordEvent(ctx, "cli", "g-claim", "task.transition", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, time.Now().UTC(), a.ID, "ready", "in_progress", eventID)
		})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if n, err := p.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("RunOnce after transition = %d, %v; want 1", n, err)
	}
	if rows := state(a.ID); len(rows) != 1 || rows[0]["s"] != "in_progress" {
		t.Fatalf("re-projected state = %v; want exactly one in_progress literal", rows)
	}
}
```

- [ ] **Step 2: Run it**

Run: `docker compose up -d postgres oxigraph && go test ./internal/projector/ -run TestProjectorEndToEnd -v`
Expected: PASS (skips if either service is down and CI is unset)

- [ ] **Step 3: Run the whole tree**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/projector/e2e_oxigraph_test.go
git commit -m "Prove the projection slice end to end against Oxigraph"
```

---

## Task 11: Run the projector under `lode serve`, document it

**Files:**
- Modify: `internal/cmd/serve.go` (env config + background loop, next to the sweeper at `internal/cmd/serve.go:91-108`)
- Test: `internal/cmd/serve_test.go` (add)
- Modify: `README.md`

- [ ] **Step 1: Write the failing test**

Append to `internal/cmd/serve_test.go` (same package as the existing serve
tests):

```go
func TestGraphProjectorFromEnv(t *testing.T) {
	t.Setenv("LODE_GRAPH_URL", "")
	t.Setenv("LODE_GRAPH_TOKEN_URL", "")
	if p := graphProjectorFromEnv(context.Background(), nil); p != nil {
		t.Fatal("projector enabled without LODE_GRAPH_URL")
	}

	t.Setenv("LODE_GRAPH_URL", "http://localhost:7878")
	if p := graphProjectorFromEnv(context.Background(), nil); p == nil {
		t.Fatal("projector nil with LODE_GRAPH_URL set")
	}
}
```

Add `"context"` to that file's imports if absent.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestGraphProjectorFromEnv`
Expected: FAIL — `undefined: graphProjectorFromEnv`

- [ ] **Step 3: Write the wiring**

In `internal/cmd/serve.go`, add:

```go
// graphProjectorFromEnv builds the knowledge-graph projector when
// LODE_GRAPH_URL (the SPARQL endpoint base) is set. LODE_GRAPH_TOKEN_URL
// plus LODE_GRAPH_CLIENT_ID/LODE_GRAPH_CLIENT_SECRET switch the client to
// Keycloak client-credentials auth (spec 009 item 4); without them the
// endpoint is called unauthenticated (dev Oxigraph).
func graphProjectorFromEnv(ctx context.Context, st *store.Store) *projector.Projector {
	base := os.Getenv("LODE_GRAPH_URL")
	if base == "" {
		return nil
	}
	var src oauth2.TokenSource
	if tokenURL := os.Getenv("LODE_GRAPH_TOKEN_URL"); tokenURL != "" {
		cfg := clientcredentials.Config{
			ClientID:     os.Getenv("LODE_GRAPH_CLIENT_ID"),
			ClientSecret: os.Getenv("LODE_GRAPH_CLIENT_SECRET"),
			TokenURL:     tokenURL,
		}
		src = cfg.TokenSource(ctx)
	}
	return projector.New(st, graph.NewClient(base, src), 200)
}
```

with these imports added to the file's import block:

```go
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/sunstoneinstitute/worklode/internal/graph"
	"github.com/sunstoneinstitute/worklode/internal/projector"
```

Then, in the serve `RunE`, directly after the sweeper goroutine
(`internal/cmd/serve.go:91-108`), add:

```go
			// Background graph projection: mirror dirty tasks into the
			// knowledge graph every 10s (spec 006 §Projection). A single
			// projector plus the per-branch write lock on graph-server
			// makes CAS unnecessary for v1 (spec 009 item 6).
			if p := graphProjectorFromEnv(ctx, st); p != nil {
				go func() {
					ticker := time.NewTicker(10 * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							if n, err := p.RunOnce(ctx); err != nil {
								slog.Error("graph projection", "err", err)
							} else if n > 0 {
								slog.Info("projected tasks", "count", n)
							}
						}
					}
				}()
			}
```

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go test ./internal/cmd/...`
Expected: PASS

- [ ] **Step 5: Document it**

Add a section to `README.md` (after the existing server configuration
material):

```markdown
## Knowledge graph projection

When `LODE_GRAPH_URL` is set, `lode serve` mirrors every task into the
Worklode knowledge graph (spec 006): a background projector follows the
`state_log` outbox and replaces each dirty task's triples in its Workstream
named graph over SPARQL Update, checkpointed in `graph_projection`.

- `LODE_GRAPH_URL` — SPARQL endpoint base exposing `/query`, `/update`,
  `/store` (data-platform graph-server in prod; the compose `oxigraph`
  service locally).
- `LODE_GRAPH_TOKEN_URL`, `LODE_GRAPH_CLIENT_ID`,
  `LODE_GRAPH_CLIENT_SECRET` — optional Keycloak client-credentials for the
  endpoint; unset means unauthenticated (dev only).

The `wl:` vocabulary sources live under `rdf/wl/` and are staged for the
rdf-registry PR. Integration tests run against the compose `oxigraph`
service (`docker compose up -d oxigraph`) and skip when it is not running.
```

- [ ] **Step 6: Run everything once more**

Run: `docker compose up -d postgres oxigraph && go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go README.md
git commit -m "Run the graph projector under lode serve"
```
