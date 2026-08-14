---
status: draft
covers:
  - spec: docs/specs/004-execution-backbone.md#sec-1.3
    coverage: partial
---
# Follow-up edges implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the backbone a third task edge, `follow_up_to`, so the loose end
an agent finds mid-task can be filed as a task that records where it came from
— instead of vanishing into `docs/follow-ups.md` or being mis-modelled as a
`child_of` child that turns its origin into a container.

**Architecture:** One new edge type in the existing `task_edges` table, carried
through the layers that already exist for `blocks` and `child_of`. It gates
nothing: `ranking.go`'s claimability query and every hierarchy query are already
type-qualified (`e.type = 'blocks'`, `c.type = 'child_of'`), so no read path
changes and the new type is invisible to them by construction. The only new
constraint is a partial unique index giving a task at most one origin, mirroring
`task_edges_single_parent`. Above the store, each layer gains the type in the
one place it enumerates edge types, plus a one-round-trip creation path
(`POST /api/v1/tasks` with `follow_up_to`, `lode task add --follow-up-to`) —
which is the case that matters, because an agent files a follow-up mid-task and
a second call is a second chance to skip it.

**Tech Stack:** Go 1.26, Postgres via `database/sql` + pgx, golang-migrate
(`deploy/base/migrations/`), `net/http` mux, cobra CLI, `templ` components in
`internal/ui`, Turtle/SHACL in `ns/`. Store and API tests need Postgres with
pgvector from `docker-compose.yml`; `store.OpenTestStore` skips silently when it
is unreachable, so verify with a reachable database or the run proves nothing.

**Read first:**
- `docs/specs/004-execution-backbone.md` §1.3 — the governing text, already
  amended in place with the `follow_up_to` bullet, the three-way split of what
  each edge type decides, and the surface this plan builds
- `internal/store/tasks.go:540` (`AddEdge`), `:625` (`RemoveEdge`), `:640`
  (`ListEdges`)
- `deploy/base/migrations/0006_task_hierarchy.up.sql` — the single-parent index
  this plan's index mirrors
- `internal/api/tasks.go:29` (`validEdgeTypes`), `:76` (`createTaskRequest`),
  `:420` (`resolveEdge`)
- `internal/cli/client.go:563` (`CreateTaskInput`), `:1105` (`Block`/`Unblock`),
  `:1115` (`Parent`/`Unparent`)
- `internal/cmd/task.go:78` (`newTaskAddCmd`), `:983` (`newTaskParentCmd`),
  `:1019` (`newTaskUnparentCmd`)
- `internal/api/web.go:229` (the two type switches), `internal/ui/views.go:91`
  (`TaskView`), `internal/ui/task.templ:41` (the Edges card)
- `ns/ontology.ttl:394` (`wl:dependsOn`/`wl:blocks`), `ns/shapes.ttl:159`
  (`wl:TaskShape`)

## Global Constraints

- **No read path may start considering the new type.** Every existing
  `task_edges` query is type-qualified — `ranking.go:21,68,74`,
  `tasks.go:495,500,748,769,795`, `hierarchy.go`, `hierarchy_resolve.go:127`,
  `brief.go:140`, `project_work.go:75,139`. Do not widen any of them. A
  follow-up must never gate a claim, never appear as a child, and never enter a
  progress roll-up.
- **Never edit a shipped migration.** Add `0018_follow_up_edges.up.sql` /
  `.down.sql`, list both in `deploy/base/kustomization.yaml`, and run
  `./scripts/check-migrations.sh --no-fix` — if another branch has claimed 0018,
  the hook renumbers, and the kustomization entry must follow.
- **`internal/model` does not exist yet.** CLAUDE.md's ADR-036 note describes a
  staged migration of the `store.Task`/`taskJSON`/`cli.Task` triplicate that has
  not landed. Follow the current per-package structs; when 036 executes, these
  fields move with the structs they live in. Do not create `internal/model` here.
- **Regenerated files are committed artifacts.** `internal/ui/*_templ.go` comes
  from `go generate ./...`; commit the regenerated file with the `.templ` change.
- **Metrics:** this plan adds no HTTP endpoint, background loop, outbound call,
  or store operation with a new outcome — `POST/DELETE /edges` and
  `POST /tasks` already exist and already carry their metrics. No new
  `worklode_*` metric is required, and none should be added.
- **Commit format:** describe the behaviour and the reason, not the plan file.
  Never add `Co-authored-by:` trailers.

## Concurrency warning

Another session is actively editing `internal/api`, `internal/cli`,
`internal/store` and `CLAUDE.md` in the main checkout for ADR 036. Execute this
plan in a git worktree off `main`, and rebase rather than merge if 036 lands
first.

---

## Task 1: The edge type exists, in the database and in the vocabulary

**Files:**
- Create: `deploy/base/migrations/0018_follow_up_edges.up.sql`
- Create: `deploy/base/migrations/0018_follow_up_edges.down.sql`
- Modify: `deploy/base/kustomization.yaml` (configMapGenerator file list)
- Modify: `internal/store/tasks.go` (`AddEdge`)
- Modify: `ns/ontology.ttl`, `ns/shapes.ttl`
- Test: `internal/store/hierarchy_test.go` (new tests beside
  `TestSingleParentIndex`), `internal/store/ranking_test.go` (one new test)

**Produces:** `store.AddEdge(tx, now, from, to, "follow_up_to")` succeeds;
a second origin out of one task returns `store.ErrEdgeExists`.

- [ ] **Step 1: Write the failing store tests**

In `internal/store/hierarchy_test.go`:

```go
// TestAddEdgeFollowUpTo checks the third edge type: it is accepted, it is not
// project-scoped the way child_of is, and it confers no parent-hood — the
// origin gains no children and no roll-up.
func TestAddEdgeFollowUpTo(t *testing.T) {
	s := openTaskStore(t)
	origin := createTask(t, s, taskTestNow, defaultTaskInput())
	followUp := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, _ int64) error {
			return AddEdge(tx, taskTestNow, followUp.ID, origin.ID, "follow_up_to")
		}); err != nil {
		t.Fatalf("AddEdge follow_up_to: %v", err)
	}

	progress, err := s.ChildProgress(t.Context(), origin.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if progress.Total != 0 {
		t.Fatalf("origin progress = %+v, want zero total: a follow-up is not a child", progress)
	}
	parent, err := s.ParentOf(t.Context(), followUp.ID)
	if err != nil {
		t.Fatalf("ParentOf: %v", err)
	}
	if parent != nil {
		t.Fatalf("follow-up parent = %+v, want nil", parent)
	}
}

// TestSingleOriginIndex pins the partial unique index: a task has at most one
// origin, whichever task the second edge points at.
func TestSingleOriginIndex(t *testing.T) {
	s := openTaskStore(t)
	followUp := createTask(t, s, taskTestNow, defaultTaskInput())
	originA := createTask(t, s, taskTestNow, defaultTaskInput())
	originB := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, _ int64) error {
			return AddEdge(tx, taskTestNow, followUp.ID, originA.ID, "follow_up_to")
		}); err != nil {
		t.Fatalf("first origin: %v", err)
	}
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, _ int64) error {
			return AddEdge(tx, taskTestNow, followUp.ID, originB.ID, "follow_up_to")
		})
	if !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("second origin error = %v, want ErrEdgeExists", err)
	}
}
```

In `internal/store/ranking_test.go` — the test that proves the edge gates
nothing:

```go
// TestClaimNextIgnoresFollowUpTo pins 004 §1.3: follow_up_to is provenance, not
// scheduling. A follow-up is claimable while its origin is wide open, which is
// exactly what separates it from blocks.
func TestClaimNextIgnoresFollowUpTo(t *testing.T) {
	s := openClaimNextStore(t)
	ctx := t.Context()
	origin := createTask(t, s, claimNextTestNow, defaultTaskInput())
	followUp := createTask(t, s, claimNextTestNow, defaultTaskInput())

	if _, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "task.edge_added", nil,
		func(tx *sql.Tx, _ int64) error {
			return AddEdge(tx, claimNextTestNow, followUp.ID, origin.ID, "follow_up_to")
		}); err != nil {
		t.Fatalf("AddEdge follow_up_to: %v", err)
	}

	blocked, err := s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if blocked[followUp.ID] {
		t.Fatal("follow-up reports blocked, want claimable: follow_up_to gates nothing")
	}

	res, err := s.ClaimNext(ctx, ClaimNextOpts{ActorID: "stig", Worktree: "h:/.worktrees/1"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !res.Claimed {
		t.Fatalf("ClaimNext claimed nothing, want one of %s/%s", origin.ID, followUp.ID)
	}
}
```

`BlockedTaskIDs` (`internal/store/tasks.go:767`) returns the map of blocked ids;
`ChildProgress` and `ParentOf` are at `internal/store/hierarchy.go:181` and
`:196`. `createTask`, `defaultTaskInput`, `nextExt`, `taskTestNow`,
`openTaskStore` and `openClaimNextStore` are existing helpers in the same
package — read `hierarchy_test.go:36` and `ranking_test.go:364` for their use.

- [ ] **Step 2: Run the tests and watch them fail**

```bash
go test ./internal/store -run 'TestAddEdgeFollowUpTo|TestSingleOriginIndex|TestClaimNextIgnoresFollowUpTo' -count=1
```

Expected: failures from `AddEdge`'s `unknown edge type "follow_up_to"` guard.
If the tests *skip*, Postgres is unreachable — start it with
`docker compose up -d` and re-run, because a skipped run proves nothing.

- [ ] **Step 3: Write the migration**

`deploy/base/migrations/0018_follow_up_edges.up.sql`:

```sql
-- The third task edge (004 §1.3): "A follow_up_to B" records that A was spun
-- out of the work on B. Provenance only -- it gates no claim and confers no
-- parent-hood, so no existing query changes: every one of them is already
-- qualified by edge type.
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;
ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('child_of','blocks','follow_up_to'));

-- A task has at most one origin, the way it has at most one parent. The origin
-- side is unbounded: one task spawns any number of follow-ups.
CREATE UNIQUE INDEX task_edges_single_origin
    ON task_edges (from_task) WHERE type = 'follow_up_to';
```

`deploy/base/migrations/0018_follow_up_edges.down.sql`:

```sql
-- Narrowing a CHECK fails on any row outside it, so the edges go first. They
-- are provenance and nothing derives from them, so dropping them loses a
-- record and breaks nothing.
DROP INDEX IF EXISTS task_edges_single_origin;
DELETE FROM task_edges WHERE type = 'follow_up_to';
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;
ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('child_of','blocks'));
```

Verify the constraint name against a live database before trusting it — the
baseline declares the CHECK inline on the column, so Postgres names it
`task_edges_type_check`, and `0017_narrow_task_kinds.up.sql` relies on the same
convention for `tasks_kind_check`. Confirm with:

```bash
docker compose exec -T postgres psql -U postgres -c \
  "\d+ task_edges" | grep -i check
```

Then add both files to `deploy/base/kustomization.yaml` after the 0017 pair:

```yaml
      - migrations/0018_follow_up_edges.up.sql
      - migrations/0018_follow_up_edges.down.sql
```

- [ ] **Step 4: Teach `AddEdge` the type**

In `internal/store/tasks.go`, widen the guard at the top of `AddEdge`:

```go
	if typ != "child_of" && typ != "blocks" && typ != "follow_up_to" {
		return fmt.Errorf("unknown edge type %q: %w", typ, ErrInvalidInput)
	}
```

Leave the `if typ == "child_of" { checkHierarchy(...) }` branch alone —
`follow_up_to` is deliberately unchecked: it is cross-project by design, and
nothing walks it transitively, so a cycle costs nothing to hold.

Add the new index to the error mapping below the insert, beside the
single-parent case:

```go
		if isUniqueViolationOn(err, "task_edges_single_origin") {
			return fmt.Errorf("task %s is already a follow-up to another task: %w",
				fromTask, ErrEdgeExists)
		}
```

- [ ] **Step 5: Mirror the term into `ns/`**

In `ns/ontology.ttl`, after the `wl:blocks` block:

```turtle
wl:followUpTo a owl:ObjectProperty, owl:FunctionalProperty ;
    wl:layer wlc:execution ;
    rdfs:domain wl:Task ;
    rdfs:range wl:Task ;
    rdfs:comment """This task was spun out of the work on that task (backbone follow_up_to,
        spec 004 §1.3). Provenance, not scheduling: deliberately NOT a subproperty of
        dct:requires and deliberately not transitive, because it implies no ordering and
        nothing walks it. Functional -- a task has at most one origin, mirroring the
        task_edges_single_origin index. No named inverse: the origin's follow-ups are a
        backwards traversal, not a second stored edge.""" .
```

In `ns/shapes.ttl`, inside `wl:TaskShape`, after the `wl:taskState` property
block:

```turtle
    sh:property [
        sh:path wl:followUpTo ;
        sh:class wl:Task ;
        sh:maxCount 1 ;
        sh:message "A Task is a follow-up to at most one origin Task (004 §1.3)." ;
    ] ;
```

Mind the Turtle punctuation: property blocks inside a shape are separated by
`;` and the last one ends the statement with `.`.

- [ ] **Step 6: Verify**

```bash
go test ./internal/store -run 'TestAddEdgeFollowUpTo|TestSingleOriginIndex|TestClaimNextIgnoresFollowUpTo' -count=1
go test ./internal/store -count=1
./scripts/check-migrations.sh --no-fix
riot --validate ns/*.ttl
```

Expected: the three new tests pass, the rest of the store suite is unchanged,
the migration check is clean, and `riot` reports no parse errors. Run the store
suite twice — the second run proves the migration is idempotent against an
existing database.

- [ ] **Step 7: Commit**

```bash
git add deploy/base/migrations/0018_follow_up_edges.*.sql \
        deploy/base/kustomization.yaml internal/store ns/
git commit -m "Add the follow_up_to task edge"
```

---

## Task 2: The API accepts the type, and creates a follow-up in one round trip

**Files:**
- Modify: `internal/api/tasks.go` (`validEdgeTypes`, `resolveEdge`'s message,
  `createTaskRequest`, `createTask`)
- Test: `internal/api/tasks_test.go`

**Consumes:** `store.AddEdge(tx, now, from, to, "follow_up_to")` from Task 1.

**Produces:** `POST /api/v1/tasks` accepts `"follow_up_to": "WL-1"`;
`POST`/`DELETE /api/v1/tasks/{id}/edges` accept `"type": "follow_up_to"`.

- [ ] **Step 1: Write the failing API tests**

In `internal/api/tasks_test.go`, following the existing helpers (`newTestServer`,
`createTaskViaAPI`, `doReq`):

```go
// TestCreateTaskWithFollowUpTo checks the one-round-trip path: the edge lands in
// the same transaction as the insert, so there is no window where the follow-up
// exists without its origin.
func TestCreateTaskWithFollowUpTo(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Origin", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
		"follow_up_to": "WL-1",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-2", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Edges struct {
			Out []struct{ To, Type string } `json:"out"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if len(got.Edges.Out) != 1 ||
		got.Edges.Out[0].Type != "follow_up_to" || got.Edges.Out[0].To != "WL-1" {
		t.Fatalf("out edges = %+v, want one follow_up_to WL-1", got.Edges.Out)
	}
}

// TestCreateTaskUnknownFollowUpTo checks the named 404, so it cannot be
// confused with the project lookup's.
func TestCreateTaskUnknownFollowUpTo(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
		"follow_up_to": "WL-99",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "WL-99") {
		t.Fatalf("body %s, want it to name the missing origin", rr.Body.String())
	}
}

// TestEdgeEndpointAcceptsFollowUpTo checks the generic edge endpoint, both
// directions of the request shape and the delete.
func TestEdgeEndpointAcceptsFollowUpTo(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Origin", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token,
		map[string]any{"to": "WL-1", "type": "follow_up_to"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WL-2/edges", token,
		map[string]any{"to": "WL-1", "type": "follow_up_to"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove edge status = %d, body %s", rr.Code, rr.Body.String())
	}
}
```

Check the existing file for the real helper signatures before writing —
`createProject`/`createTaskViaAPI`/`doReq` are used throughout
`internal/api/tasks_test.go` and `web_test.go`, and task ids are assigned
`WL-1`, `WL-2`, … in creation order.

- [ ] **Step 2: Run the tests and watch them fail**

```bash
go test ./internal/api -run 'FollowUpTo' -count=1
```

Expected: 422 `invalid edge type` on the edge endpoint, and the created task
carrying no edges on the create path (the unknown field is ignored today).

- [ ] **Step 3: Implement**

In `internal/api/tasks.go`:

```go
var validEdgeTypes = map[string]bool{
	"blocks": true, "child_of": true, "follow_up_to": true,
}
```

and the matching message in `resolveEdge`:

```go
		writeErr(w, http.StatusUnprocessableEntity,
			"invalid edge type: must be blocks, child_of, or follow_up_to")
```

Add the field to `createTaskRequest`, beside `Parent`:

```go
	FollowUpTo string `json:"follow_up_to"`
```

In `createTask`, trim it beside `req.Parent`, give it the same named-404
pre-check, and wire the edge in the same apply callback:

```go
	req.FollowUpTo = strings.TrimSpace(req.FollowUpTo)
```

```go
	if req.FollowUpTo != "" {
		// Named 404 for the same reason as Parent's: AddEdge's ErrNotFound
		// would otherwise be reported as an anonymous 404 indistinguishable
		// from the project lookup's.
		if _, err := s.st.GetTask(r.Context(), req.FollowUpTo); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "follow_up_to not found: "+req.FollowUpTo)
			return
		}
	}
```

```go
			if req.FollowUpTo != "" {
				if err := store.AddEdge(tx, now, t.ID, req.FollowUpTo, "follow_up_to"); err != nil {
					return err
				}
			}
```

`parent` and `follow_up_to` are independent — a task may legitimately be both a
child of one task and a follow-up to another — so do not make them mutually
exclusive.

- [ ] **Step 4: Verify**

```bash
go test ./internal/api -run 'FollowUpTo' -count=1
go test ./internal/api -count=1
```

Expected: the new tests pass and the existing API suite is unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Accept follow_up_to on the task and edge endpoints"
```

---

## Task 3: The CLI files and unfiles a follow-up

**Files:**
- Modify: `internal/cli/client.go` (`CreateTaskInput`, new `FollowUp` /
  `Unfollow` methods)
- Modify: `internal/cmd/task.go` (`newTaskAddCmd`, two new commands, and the
  `newTaskCmd` command list around `:43`)
- Test: `internal/cli/client_test.go`

**Consumes:** the API surface from Task 2.

**Produces:** `lode task add --follow-up-to <id>`,
`lode task follow-up <id> --of <id>`, `lode task unfollow-up <id>`.

- [ ] **Step 1: Write the failing client test**

In `internal/cli/client_test.go`, copy the shape of
`TestClientBlockUnblock` (`:385`) into a `TestClientFollowUpUnfollow`: assert
that `FollowUp` issues `POST /api/v1/tasks/WL-2/edges` with body
`{"to":"WL-1","type":"follow_up_to"}`, and that `Unfollow` issues the same body
with `DELETE`.

- [ ] **Step 2: Run it and watch it fail**

```bash
go test ./internal/cli -run 'FollowUp' -count=1
```

Expected: compile failure — `FollowUp` undefined.

- [ ] **Step 3: Implement the client**

In `internal/cli/client.go`, add to `CreateTaskInput` beside `Parent`:

```go
	// FollowUpTo, when set, records the task this one was spun out of in the
	// same request instead of a separate edge call.
	FollowUpTo string `json:"follow_up_to,omitempty"`
```

and the two methods beside `Parent`/`Unparent`:

```go
// FollowUp calls POST /api/v1/tasks/{id}/edges to record that id was spun out
// of the work on origin.
func (c *Client) FollowUp(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &origin, Type: "follow_up_to"})
}

// Unfollow calls DELETE /api/v1/tasks/{id}/edges to drop the follow-up edge
// from id to origin.
func (c *Client) Unfollow(ctx context.Context, id, origin string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/api/v1/tasks/"+url.PathEscape(id)+"/edges",
		edgeBody{To: &origin, Type: "follow_up_to"})
}
```

- [ ] **Step 4: Implement the commands**

In `internal/cmd/task.go`, extend `newTaskAddCmd`: add a `followUpTo` string to
the `var` block, pass `FollowUpTo: followUpTo` in the `cli.CreateTaskInput`
literal, and register the flag beside `--parent`:

```go
	cmd.Flags().StringVar(&followUpTo, "follow-up-to", "",
		"record that this task was spun out of the work on that task")
```

Then two new commands, modelled exactly on `newTaskParentCmd` /
`newTaskUnparentCmd`:

```go
func newTaskFollowUpCmd() *cobra.Command {
	var of string
	cmd := &cobra.Command{
		Use:   "follow-up <id>",
		Short: "Record that a task was spun out of the work on another task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			of, err = resolveTaskID(cmd.Context(), of, c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.FollowUp(cmd.Context(), id, of)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now a follow-up to %s\n", id, of)
			return nil
		},
	}
	cmd.Flags().StringVar(&of, "of", "", "id of the task this one was spun out of (required)")
	cmd.MarkFlagRequired("of")
	return cmd
}

func newTaskUnfollowUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unfollow-up <id>",
		Short: "Drop a task's follow-up edge to its origin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			// The edge is identified by both endpoints and the caller knows
			// only one, so read the origin back first.
			t, _, err := c.GetTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			var origin string
			for _, e := range t.Edges.Out {
				if e.Type == "follow_up_to" {
					origin = e.To
					break
				}
			}
			if origin == "" {
				return fmt.Errorf("%s is not a follow-up to anything", id)
			}
			raw, err := c.Unfollow(cmd.Context(), id, origin)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer a follow-up to %s\n", id, origin)
			return nil
		},
	}
	return cmd
}
```

Register both in the `newTaskCmd` subcommand list beside `newTaskParentCmd()`.

`lode task show` needs no change: `internal/cli/render.go:123` already prints
every edge with its type.

- [ ] **Step 5: Verify**

```bash
go test ./internal/cli ./internal/cmd -count=1
go build ./... && go vet ./...
```

Expected: green. Then eyeball the help text:

```bash
go run ./cmd/lode task follow-up --help
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli internal/cmd
git commit -m "Add follow-up CLI verbs and --follow-up-to"
```

---

## Task 4: The task page shows both directions

**Files:**
- Modify: `internal/ui/views.go` (`TaskView`)
- Modify: `internal/ui/task.templ` (the Edges card)
- Modify: `internal/ui/task_templ.go` (regenerated, not hand-edited)
- Modify: `internal/api/web.go` (`taskPage`'s two type switches)
- Test: `internal/api/web_test.go`

**Consumes:** the edges the API already returns from `store.ListEdges`.

**Produces:** `/tasks/{id}` renders "Follow-up to" and "Follow-ups" rows.

- [ ] **Step 1: Write the failing page test**

In `internal/api/web_test.go`, beside `TestTaskPageShowsProgress`:

```go
// TestTaskPageShowsFollowUps checks both directions of the provenance edge
// render on the task page: the origin lists its follow-ups, the follow-up
// names its origin.
func TestTaskPageShowsFollowUps(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Origin", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
		"follow_up_to": "WL-1",
	})

	rr := doReq(t, h, "GET", "/tasks/WL-2", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("follow-up page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Follow-up to", `/tasks/WL-1`)

	rr = doReq(t, h, "GET", "/tasks/WL-1", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("origin page status = %d, body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, rr.Body.String(), "Follow-ups", `/tasks/WL-2`)
}
```

If the web tests in this checkout require an authenticated session (the
`LODE_WEB_OPEN` work may have landed by then), copy whatever the neighbouring
page tests do for the anonymous GET rather than inventing a third way.

- [ ] **Step 2: Run it and watch it fail**

```bash
go test ./internal/api -run TestTaskPageShowsFollowUps -count=1
```

Expected: the strings are absent from the rendered page.

- [ ] **Step 3: Extend the view type and the component**

In `internal/ui/views.go`, add to `TaskView` after `Children`:

```go
	FollowUpTo string
	FollowUps  []string
```

In `internal/ui/task.templ`, add two `<li>` items after the Children item,
matching the existing style exactly:

```html
							<li>Follow-up to:
								if v.FollowUpTo == "" {
									<span class="muted">none</span>
								} else {
									<a href={ "/tasks/" + v.FollowUpTo }>{ v.FollowUpTo }</a>
								}
							</li>
							<li>Follow-ups:
								if len(v.FollowUps) == 0 {
									<span class="muted">none</span>
								} else {
									for _, id := range v.FollowUps {
										<a href={ "/tasks/" + id }>{ id }</a>{ " " }
									}
								}
							</li>
```

Regenerate:

```bash
go generate ./...
```

- [ ] **Step 4: Fill the fields in `web.go`**

In `internal/api/web.go`'s `taskPage`, add a case to each switch:

```go
	for _, e := range out {
		switch e.Type {
		...
		case "follow_up_to":
			view.FollowUpTo = e.ToTask
		}
	}
	for _, e := range in {
		switch e.Type {
		...
		case "follow_up_to":
			view.FollowUps = append(view.FollowUps, e.FromTask)
		}
	}
```

- [ ] **Step 5: Verify**

```bash
go test ./internal/api -run TestTaskPageShowsFollowUps -count=1
go test ./internal/api ./internal/ui -count=1
git diff --stat internal/ui/task_templ.go
```

Expected: the new test passes, the suites are unchanged, and `task_templ.go`
shows a regenerated diff (never a hand edit).

- [ ] **Step 6: Commit**

```bash
git add internal/ui internal/api
git commit -m "Show follow-up edges on the task page"
```

---

## Task 5: End-to-end through public surfaces

**Files:**
- Create: `e2e/followup_test.go`

**Consumes:** every layer above.

- [ ] **Step 1: Write the test**

Model it on `e2e/hierarchy_test.go` — same build tag, same server/actor/token
setup through `cli.Client` and no direct store writes:

```go
//go:build e2e

package e2e

// TestFollowUpLoop exercises 004 §1.3 end-to-end through public surfaces only:
// an agent working a task files a follow-up in one call, the edge shows on both
// task pages, and — the property that separates follow_up_to from blocks — the
// follow-up is claimable while its origin is still open.
```

The test should:

1. Create a project, an actor, and a token, exactly as `TestHierarchyLoop` does.
2. Create the origin task, then create the follow-up with
   `cli.CreateTaskInput{..., FollowUpTo: origin.ID}`.
3. `GetTask` the follow-up and assert one out-edge, `follow_up_to` → origin.
4. Claim with `ClaimNext` twice and assert both tasks are claimable — the
   follow-up is not gated by its open origin.
5. `GET /tasks/{id}` for both ids over plain HTTP and assert the page bodies
   contain "Follow-up to" and "Follow-ups" respectively.
6. `Unfollow` the follow-up and assert the out-edge is gone.

- [ ] **Step 2: Run it**

```bash
go test -race -count=1 -tags e2e ./e2e/ -run TestFollowUpLoop
```

Expected: pass. If it skips, Postgres is unreachable — start it and re-run.

- [ ] **Step 3: Commit**

```bash
git add e2e/followup_test.go
git commit -m "Cover the follow-up edge end to end"
```

---

## Verification

Run all of it from a clean tree with Postgres up:

```bash
docker compose up -d
go build ./... && go vet ./...
go test -race -count=1 ./...
go test -race -count=1 -tags e2e ./e2e/
./scripts/check-migrations.sh --no-fix
./scripts/secfmt.py -l
./scripts/secmeta.py
riot --validate ns/*.ttl
git status --short   # expect nothing but intended files; go generate must be a no-op
```

The specific claims this plan has to be able to back:

- A follow-up of an open origin is claimable (`TestClaimNextIgnoresFollowUpTo`,
  and step 4 of the e2e test).
- An origin gains no children and no roll-up (`TestAddEdgeFollowUpTo`).
- A task has at most one origin (`TestSingleOriginIndex`).
- `lode task add --follow-up-to` costs one round trip
  (`TestCreateTaskWithFollowUpTo`).
- Both directions render (`TestTaskPageShowsFollowUps`).

## Follow-ups this plan deliberately does not close

- **The `lode` plugin does not know about the verb yet.** The point of the edge
  is that an agent that finds a loose end mid-task files it against the task it
  came from. Making that happen means teaching `plugins/lode/` — the
  `lode-worker` agent and the `/lode:*` commands — to reach for
  `--follow-up-to`. That is a separate change to a separate surface.
- **`docs/follow-ups.md` stays where it is.** Migrating this repo's own
  follow-up list into tasks with `follow_up_to` edges is a data question, not a
  code one.
- **No board or cockpit surface.** The edge appears on the task page only.
  Whether a project's cockpit should show "follow-ups filed against closed work"
  is a spec 032 question and is not asked here.
- **No graph projection.** `wl:followUpTo` exists in `ns/`, but the
  backbone→graph projection (spec 006) is not extended to emit it.
