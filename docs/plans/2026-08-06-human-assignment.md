---
status: accepted
---
# Human assignment: assignee, lease-free start, Keycloak login

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A human can own a task without holding a lease — `lode task assign
/ start / stop / submit` drive a lease-free lifecycle, the board and task
pages show who is on what, and staff log in to the web UI through the org
Keycloak.

**Architecture:** `tasks.assignee` is a nullable FK to `actors(id)`,
independent of leases: a lease answers "who may write to this worktree right
now", the assignee answers "who owns this work". Humans move
`ready → in_progress → in_review → merged` through new endpoints plus one
`patchStateFrom` entry; the existing claim/lease path is untouched and stays
the agent path. Every write goes through `RecordEvent` (source `cli`) like
all task mutations.

**No governing spec.** This plan anticipates the research-work core-model
spec (milestones, deliverables, approvals — being written next); assignment
is needed for the 2026-08-11 data-scientist workshop and is mechanical
enough to plan directly. Spec 028 §2 independently requires an assignee for
escalation, so the column is convergent with the accepted corpus, not a
detour. When the core-model spec lands it adopts this plan's semantics.

**Tech stack:** Go 1.25+, Postgres via golang-migrate + `database/sql`,
Prometheus client, cobra CLI, html/template web pages.

**Read first:**
- `internal/store/leases.go:130-222` (`Claim` — the actor-existence check
  and FOR UPDATE transaction shape to copy)
- `internal/store/events.go:34` (`RecordEvent` — every write goes through it)
- `internal/api/tasks.go:303-321` (`patchTaskRequest`, `patchStateFrom` —
  the map this plan extends)
- `internal/api/lifecycle.go:237-272` (`finishTask` — the
  RecordEvent-then-GetTask handler shape to copy)
- `internal/api/metrics.go` + `docs/specs/022-prometheus-metrics.md`
  (nil-safe metrics struct convention)
- `docs/specs/018-task-hierarchy.md` §3 (states an epic may not take —
  assignment must respect the same terminal set)

## Global constraints

- Every store mutation goes through `store.RecordEvent(ctx, "cli", …)` with
  a `randomExternalID()`; the apply callback does the writes in one tx.
- New endpoints/store ops with meaningful outcomes need `worklode_*`
  Prometheus metrics in the owning package, with tests (CLAUDE.md, spec 022).
- Migration number `0010` is provisional — `./scripts/check-migrations.sh`
  renumbers on collision; the pair must also be listed in
  `deploy/base/kustomization.yaml`.
- Terminal states (no assignment changes, no start/stop): `merged`,
  `deployed_dev`, `deployed_prod`, `released`, `abandoned`.
- Epics (`kind = 'epic'`) are never assignable or startable — same guard as
  `Claim` (`internal/store/leases.go:155-159`).
- Store tests need Postgres with pgvector (`TEST_POSTGRES_DSN`); they skip
  silently without it unless `CI` is set — run them against a live database.

## Semantics (shared by every task below)

| Act | Guard | Effect |
|---|---|---|
| assign | task exists, not terminal, not epic; target actor exists | `assignee = <actor>`; event `task.assigned` |
| unassign | task exists, not terminal | `assignee = NULL`; event `task.unassigned` |
| start | state `ready`, not epic; assignee is caller or NULL (NULL ⇒ auto-assign caller); task not blocked | `ready → in_progress`, **no lease**; event `task.started` |
| stop | state `in_progress`; caller is assignee; **no active lease** (a leased task stops via `release`) | `in_progress → ready`; assignee kept; event `task.stopped` |
| submit | state `in_progress` (existing `Transition` enforces) | `in_progress → in_review` via `PATCH state=in_review`; event `task.updated` |

Unassigned in Go is `Assignee == ""` (scan `COALESCE(assignee, '')`),
matching the `Concern` convention. Guard violations return
`store.ErrInvalidInput` (→ 422) except missing task/actor
(`store.ErrNotFound` → 404).

**Non-goals:** milestones/deliverables/approvals/participants (core-model
spec); `githubUsername` correlation (week after the workshop); TTL changes
(moot — humans hold no lease); guarding `claim` on assigned tasks (an agent
may still claim a human-assigned task; revisit in the core-model spec);
rendering display names instead of actor ids in the web UI; shell
completion and statusline.

## Tasks

### Task 1 — Migration: `tasks.assignee`

```yaml
kind: chore
priority: high
blockedBy: [ ]
```

Add the column and partial index.

`deploy/base/migrations/0010_task_assignee.up.sql`:

```sql
-- Spec-less plan 2026-08-06-human-assignment: a human owns a task without
-- holding a lease. NULL = unassigned. Partial index backs "my tasks".
ALTER TABLE tasks ADD COLUMN assignee text REFERENCES actors (id);
CREATE INDEX tasks_assignee ON tasks (assignee) WHERE assignee IS NOT NULL;
```

`deploy/base/migrations/0010_task_assignee.down.sql`:

```sql
DROP INDEX tasks_assignee;
ALTER TABLE tasks DROP COLUMN assignee;
```

- [ ] Write both files.
- [ ] Add both filenames to the `worklode-migrations` configMapGenerator
      list in `deploy/base/kustomization.yaml` (keep numeric order).
- [ ] Run `./scripts/check-migrations.sh --no-fix` — expect pass.
- [ ] Run `go test ./internal/store -run TestClaim -count=1` against local
      Postgres — proves the schema still loads.
- [ ] Commit: `Add tasks.assignee migration`

### Task 2 — Store: assignee on `Task`, assign/unassign/start/stop

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:** create `internal/store/assign.go` +
`internal/store/assign_test.go`; modify `internal/store/tasks.go` (struct +
scans).

**Produces (later tasks consume):**

```go
// Task gains: Assignee string   // "" = unassigned
func AssignTask(tx *sql.Tx, now time.Time, id, assignee string, eventID int64) error
func UnassignTask(tx *sql.Tx, now time.Time, id string, eventID int64) error
// StartTask returns the assignee it settled on (caller when auto-assigned).
func StartTask(tx *sql.Tx, now time.Time, id, actorID string, eventID int64) (string, error)
func StopTask(tx *sql.Tx, now time.Time, id, actorID string, eventID int64) error
```

Implementation notes:

- Thread `Assignee` through `Task`, every `SELECT` in `GetTask` /
  `ListTasks` (`COALESCE(assignee, '')`), and `taskColumns`-style helpers if
  present. `CreateTask` leaves it NULL.
- Add `Assignee string` to `TaskFilter` (`ListTasks` adds
  `AND assignee = $n` when set) — backs `lode task list --mine` (Task 4).
- `AssignTask`: verify the target actor exists first (copy the check in
  `Claim`, `internal/store/leases.go:160-167`, returning
  `fmt.Errorf("actor %s: %w", assignee, ErrNotFound)`), then
  `SELECT state, kind FROM tasks WHERE id = $1 FOR UPDATE`; reject terminal
  states and `kind = 'epic'` with `ErrInvalidInput`; `UPDATE`, then
  `LogChange(tx, "task", id, eventID, map[string]string{"field":
  "assignee", "new": assignee})`.
- `StartTask`: `FOR UPDATE` read of `state, kind, COALESCE(assignee,'')`;
  reject epics; reject `assignee != "" && assignee != actorID`
  (`ErrInvalidInput`, message `assigned to <a>; unassign first`); reject
  blocked tasks via the same `IsBlocked` check `Claim` uses; set assignee
  when empty (with `LogChange`); `Transition(tx, now, id, "ready",
  "in_progress", eventID)`.
- `StopTask`: `FOR UPDATE`; require state `in_progress` and caller ==
  assignee; require no active lease
  (`SELECT 1 FROM leases WHERE task_id = $1 AND released_at IS NULL` →
  `ErrInvalidInput`, message `held by an active lease; use release`);
  `Transition` back to `ready`.

Tests (`assign_test.go`, table-driven, against the test DB like
`leases_test.go`): assign happy path + round-trip via `GetTask`; assign to
missing actor → `ErrNotFound`; assign on merged task → `ErrInvalidInput`;
assign on epic → `ErrInvalidInput`; unassign clears; start auto-assigns
when unassigned; start on someone else's task → `ErrInvalidInput`; start on
blocked task → `ErrInvalidInput`; stop by non-assignee → `ErrInvalidInput`;
stop while leased → `ErrInvalidInput`; `ListTasks` with
`TaskFilter{Assignee: …}` filters.

- [ ] Write the failing tests; run
      `go test ./internal/store -run TestAssign -count=1` — expect FAIL.
- [ ] Implement; run again — expect PASS.
- [ ] `go test ./internal/store -count=1` — full package green.
- [ ] Commit: `Add task assignment to the store`

### Task 3 — API: assign/unassign/start/stop endpoints, submit via PATCH, metrics

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:** create `internal/api/assign.go` + `internal/api/assign_test.go`;
modify `internal/api/tasks.go` (`taskJSON`, `patchStateFrom`),
`internal/api/server.go` (routes), `internal/api/metrics.go` (+ its test).

**Produces:**

```
POST /api/v1/tasks/{id}/assign    body {"assignee": "..."} (optional; default caller)
POST /api/v1/tasks/{id}/unassign  no body
POST /api/v1/tasks/{id}/start     no body
POST /api/v1/tasks/{id}/stop      no body
PATCH /api/v1/tasks/{id}          "state": "in_review" now legal (from in_progress)
```

`taskJSON` gains an `Assignee string` field with json tag `assignee` (set
in `toTaskJSON`) — this propagates to the board JSON automatically because
`boardTaskJSON` embeds `taskJSON`.

Each handler follows the `finishTask` shape (`internal/api/lifecycle.go:237`):
`randomExternalID()` → `RecordEvent(ctx, "cli", extID, <event>, payload,
apply)` → `GetTask` → `writeJSON`. Event types: `task.assigned`,
`task.unassigned`, `task.started`, `task.stopped`. The caller comes from
`actorFrom(r)`.

`patchStateFrom` gains one entry — the map keys stay unique:

```go
var patchStateFrom = map[string]string{
	"ready":       "draft",
	"in_progress": "in_review",
	"in_review":   "in_progress", // human submit-for-review; PR flow sets it via webhook
}
```

Update the 422 message in `patchTask` to name the third transition. Note in
a comment that the PR webhook path (`internal/hooks/github.go:358-367`)
stays the automatic route for code tasks; PATCH is the manual route for
tasks with no PR.

Metrics (spec 022 conventions, extend the existing struct in
`internal/api/metrics.go`):

```go
assignments *prometheus.CounterVec // worklode_task_assignments_total{action}
```

`action` ∈ `assign|unassign|start|stop` (bounded), incremented on success
only; nil-safe like the existing fields; test in
`internal/api/metrics_internal_test.go` pattern.

Tests (`assign_test.go`, using `newTestServer` from `server_test.go`):
assign defaults to caller; assign explicit body; unassign; start
auto-assigns and moves to `in_progress` with **no lease row**; start on a
task assigned to another actor → 422; stop → back to `ready`; stop on a
claimed task → 422; `PATCH state=in_review` from `in_progress` → 200, from
`ready` → 422; full human lifecycle assign→start→submit→done ends `merged`.

- [ ] Failing tests → implement → `go test ./internal/api -count=1` green.
- [ ] Commit: `Add assignment and lease-free start to the API`

### Task 4 — CLI: `lode task assign/unassign/start/stop/submit`, `--mine`

```yaml
kind: feature
priority: high
blockedBy: [3]
```

**Files:** modify `internal/cli/client.go`, `internal/cmd/task.go`, plus
their tests (`internal/cli/client_test.go`, `internal/cmd/` test files
follow existing patterns for each verb — crib from `release`/`rework`).

Client methods (follow `taskAction`, `internal/cli/client.go:903`):

```go
func (c *Client) AssignTask(ctx context.Context, id, assignee string) (Task, []byte, error) // assignee "" = self
func (c *Client) UnassignTask(ctx context.Context, id string) (Task, []byte, error)
func (c *Client) StartTask(ctx context.Context, id string) (Task, []byte, error)
func (c *Client) StopTask(ctx context.Context, id string) (Task, []byte, error)
func (c *Client) SubmitTask(ctx context.Context, id string) (Task, []byte, error) // patchTaskState(id, "in_review")
```

Subcommands on `newTaskCmd` (`internal/cmd/task.go:24`), each using
`resolveTaskID` so bare numbers work:

- `lode task assign <id> [--to <actor>]` — default self ("assign to me").
- `lode task unassign <id>`
- `lode task start <id>` — help text: "Start working on a task you own
  (assigns you if unassigned). No worktree, no lease — for agent claims use
  `lode task claim`."
- `lode task stop <id>` — "Put a started task back to ready; keeps the
  assignment."
- `lode task submit <id>` — "Move your in-progress task to review."
- `lode task list --mine` — resolves the caller's actor id via whoami
  (reuse however `lode whoami`/login stores identity; if no such helper
  exists, `--assignee <actor>` only) plus `--assignee <actor>`; both set
  the new `assignee` query param on `GET /api/v1/tasks`. Wire that param
  through the list handler to `TaskFilter.Assignee` in this task.

Task list/show output: add an `assignee` line to `task show` and an
assignee column to `task list` rendering, matching existing column style.

- [ ] Client + command tests (httptest fixtures like neighbors) → green
      `go test ./internal/cli ./internal/cmd -count=1`.
- [ ] Commit: `Add assignment verbs to the CLI`

### Task 5 — Web UI: assignee on board and task pages

```yaml
kind: feature
priority: medium
blockedBy: [3]
```

**Files:** modify `internal/api/templates/board.html`,
`internal/api/templates/task.html`, `internal/api/web_test.go`.

`board.html`: every task table (in-progress, in-review, ready, blocked)
gains an `<th>Assignee</th>` column with `<td>{{.Assignee}}</td>` (field
promotion through the embedded `taskJSON` works in html/template). The
in-progress table keeps its `Holder` column — holder is the lease, assignee
is the owner; for a human-started task Holder is empty and Assignee is not,
which is correct and demonstrates the distinction.

`task.html`: after the created/updated line
(`internal/api/templates/task.html:9`) add:

```html
{{if .Task.Assignee}}<p class="muted">Assigned to {{.Task.Assignee}}</p>{{end}}
```

`web_test.go`: extend the board and task page tests — create a task,
assign+start it via the store/API helpers the file already uses, assert the
rendered HTML contains the assignee id in both pages.

- [ ] Tests → implement → `go test ./internal/api -run TestWeb -count=1`.
- [ ] Commit: `Show assignee on the board and task pages`

### Task 6 — Keycloak: `worklode` client and roles in the org realm

```yaml
kind: chore
priority: critical
skills:
  - sunstone-devops:app-deployment
blockedBy: [ ]
```

**Cross-repo:** this task is executed in the provisioning repo (see the
`sunstone-devops:app-deployment` skill — "Keycloak SSO/RBAC wiring for new
apps"), following how existing apps' OIDC clients are declared there.

- Public client `worklode` (authorization code + PKCE — the server reads
  only `LODE_OIDC_ISSUER`/`LODE_OIDC_CLIENT_ID`, no client secret:
  `internal/cmd/serve.go:85-86`).
- Redirect URIs: `https://worklode.dev.sunstoneinstitute.ai/auth/callback`
  and the prod equivalent.
- Client roles `user` and `admin`, delivered via the client-roles-as-groups
  mapper — `internal/oidc/oidc.go:22-27` documents the expected claim
  shape. Grant `user` to all staff (or the staff group), `admin` to
  operators.
- Explicitly **out**: the `githubUsername` attribute mapper (next week's
  correlation work).
- [ ] Client + roles land in the provisioning repo and reconcile.
- [ ] Verify: `curl https://<issuer>/.well-known/openid-configuration`
      resolves.

### Task 7 — Deploy: point worklode at Keycloak (hzdev, then hzprod)

```yaml
kind: chore
priority: critical
blockedBy: [6]
```

**Files:** `deploy/overlays/hzdev/kustomization.yaml`,
`deploy/overlays/hzprod/kustomization.yaml` — extend the existing
`worklode-config` ConfigMap patch (the `LODE_PUBLIC_URL` pattern at
`deploy/overlays/hzdev/kustomization.yaml:24-26`):

```yaml
- op: add
  path: /data/LODE_OIDC_ISSUER
  value: "<realm issuer URL — read it from the provisioning repo's existing OIDC client config, Task 6>"
- op: add
  path: /data/LODE_OIDC_CLIENT_ID
  value: "worklode"
```

`LODE_SESSION_SECRET` already ships via the ExternalSecret
(`deploy/overlays/hzdev/externalsecret-worklode-secrets.yaml:52`), so no
secret changes. Setting these envs activates `webAuth`: the web pages
switch from open to session-gated (`internal/api/web.go:1-8`) — that is the
point, and it is why hzdev goes first.

- [ ] hzdev overlay → merge → Flux reconciles → log in at
      `https://worklode.dev.sunstoneinstitute.ai/` with a **fresh** Keycloak
      account: Keycloak redirect, consent, board renders, actor row created
      (`lode`-side check: the actor appears with the `user` role).
- [ ] Walk the flow once more in an incognito window and note every screen
      — this becomes the workshop's 1Password "log in with" item.
- [ ] hzprod overlay → merge → same verification.
- [ ] Commit(s): `Enable Keycloak login on hzdev` / `…hzprod`

### Task 8 — e2e: the human lifecycle through public surfaces

```yaml
kind: feature
priority: medium
blockedBy: [4]
```

**Files:** add a test to `e2e/` (build tag `e2e`, HTTP-only per the suite's
rule — no direct store writes).

One test: create project + task via `/api/v1`, `assign` (explicit body),
`start`, assert `GET /api/v1/board` shows the task under `in_progress` with
`assignee` set and **no holder**; `PATCH state=in_review`; `done`; assert
final state `merged`. Then a negative: second actor's `start` on an
assigned `ready` task → 422.

- [ ] `go test -race -count=1 -tags e2e ./e2e/` green.
- [ ] Commit: `e2e: human assign/start/submit/done lifecycle`
