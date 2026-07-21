# Lodespar spec 01 — Execution backbone

**Date:** 2026-07-21 · **Status:** spec · **Umbrella:** `00-umbrella-architecture.md`
(shared conventions binding). Source decisions: D1–D3, D8, D11, D12, D14 of
`../2026-07-21-work-tracker-platform-graph-design.md`.

---

## Purpose & scope

The execution backbone is the ACID core Lodespar's pickup loop turns on: task state,
worktree-bound leases, the append-only event log, and the two edge types
(`blocks`, `child_of`) that gate what is claimable. It runs on **Postgres** (D2), with
the lease bound to **git-worktree identity** (D8/D11/D14).

**Concurrency.** Postgres lets N `claim --next` calls proceed concurrently, serializing
only on the specific task row(s) they actually contend for — the write throughput D8
requires for 24/7 parallel agents.

**In scope:** the Postgres schema baseline, row-lock transaction semantics, the task
state machine (incl. reopen), worktree-bound lease lifecycle, the atomic `claim`
transaction (the transaction only — not the ranking), the event log + provenance, and
the two gating edges with cycle detection.

**Out of scope (reference by title, do not duplicate):**
`claim --next` **ranking**, `concern`, `focus`, `--strict-focus`, `needs-decomposition`
→ **spec 02 (Prioritization & pickup)**. RDF vocabulary, IRI scheme, backbone→graph
projection → **spec 03 (Knowledge graph)**. Observed-layer derivers and drift queries
→ **spec 04 (Drift & overview)**. Hooks, slash commands, worktree naming/creation,
auto-resume → **spec 05 (Lodespar plugin)**. The observed/projection tables in the same
database (`issues`, `pull_requests`, `ci_runs`, `reviews`, `artifacts`, `deployments`,
`runtime_events`) have their **semantics** owned by 03/04; this spec only guarantees
they exist in the Postgres schema.

---

## Data model

Target Postgres schema for the backbone tables this spec owns. Conventions:
**`timestamptz`** timestamps; **`bigint GENERATED ALWAYS AS IDENTITY`** keys; **`boolean`**
flags; partial unique indexes and `CHECK` constraints.

### tasks

```sql
CREATE TABLE tasks (
    id         text PRIMARY KEY,                    -- WT-<n>
    project_id text NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    title      text NOT NULL,
    body       text,
    priority   text NOT NULL CHECK (priority IN ('critical','high','medium','low')),
    kind       text NOT NULL CHECK (kind IN ('feature','bug','chore','spec')),
    state      text NOT NULL CHECK (state IN
                 ('draft','ready','in_progress','in_review','done','abandoned')),
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
```

`task_seq` (single-row counter) is retained verbatim: `UPDATE task_seq SET next =
next + 1 WHERE id = 1 RETURNING next - 1` is valid Postgres and preserves the gapless
`WT-<n>` allocation semantics. (A bare `SEQUENCE` was rejected: it gaps on rollback
and complicates the `WT-` prefix. Keeping the table is a zero-behavior-change carry.)

### Task state machine

```
draft ──▶ ready ──▶ in_progress ──▶ in_review ──▶ done
              ▲          │  ▲            │
              └──────────┘  └────────────┘
           (release/expiry)   (rework)

any non-terminal ──▶ abandoned
done / abandoned ──▶ ready            (reopen — NEW in this spec)
```

Legal transitions (the `legalTransitions` map):

| from | to | trigger |
|---|---|---|
| draft | ready | publish (make claimable) |
| ready | in_progress | **claim** (lease acquired) |
| in_progress | in_review | submit for review |
| in_progress | ready | **release / lease expiry** |
| in_review | done | accept |
| in_review | in_progress | rework |
| draft·ready·in_progress·in_review | abandoned | abandon |
| **done | ready** | **reopen** (NEW) |
| **abandoned | ready** | **reopen/revive** (NEW) |

`done` and `abandoned` are no longer strictly terminal: **reopen** returns either to
`ready` (re-enters the pickup loop cleanly; a fresh claim is then required). Reopen
targets `ready`, not `in_progress`, so re-entry always goes through a lease — no task
is `in_progress` without a live lease. Reopen is its own event (`task.reopened`) and
`state_log` entry. *(Open Q1: confirm `done→ready` vs `done→in_progress`.)*

`Transition(tx, now, taskID, from, to, eventID)` stays the guard: it verifies the
membership in the transition set **and** that the task's current state equals `from`,
atomically inside the caller's tx, then bumps `updated_at` and appends a `state_log`
row attributed to `eventID`. Unknown task → `ErrNotFound`; wrong from-state →
`ErrBadTransition`.

### leases (rebound to the worktree)

The lease is keyed by **git-worktree identity, not `session_id`** (D8/D11/D14). It
outlives any single session and dies with the worktree.

```sql
CREATE TABLE leases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text NOT NULL REFERENCES tasks(id)  ON DELETE RESTRICT,
    actor_id    text NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
    worktree    text NOT NULL,          -- was session_id; canonical worktree identity
    acquired_at timestamptz NOT NULL,
    renewed_at  timestamptz,
    expires_at  timestamptz NOT NULL,
    released_at timestamptz
);

-- At most one active (unreleased) lease per task — the claim-race backstop.
CREATE UNIQUE INDEX leases_active ON leases (task_id) WHERE released_at IS NULL;

-- At most one active lease per worktree — a worktree holds one task at a time.
CREATE UNIQUE INDEX leases_active_worktree ON leases (worktree) WHERE released_at IS NULL;
```

`worktree` is an **opaque, stable identity string** the plugin supplies; the backbone
never parses it. Recommended canonical form `<host>:<abs-worktree-root>` (deterministic
worktree name `wt/<id>-<slug>` from D14 makes `/lode-resume`'s lookup trivial). Exact
composition is fixed in **spec 05**; the backbone only requires it be stable for a
worktree's lifetime and unique per live worktree. *(Open Q2.)*

`DefaultLeaseTTL = 2h` (unchanged). Renewal is a **commit-cadence heartbeat**
(D8): the `PreToolUse`/git `pre-commit` hook calls `renew` before each commit-batch —
no wall-clock timer. Missing the heartbeat (session died, machine slept) lets the
lease lapse; the sweeper reclaims it.

### task_edges

```sql
CREATE TABLE task_edges (
    from_task  text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    to_task    text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    type       text NOT NULL CHECK (type IN ('child_of','blocks')),
    created_at timestamptz NOT NULL,
    UNIQUE (from_task, to_task, type)
);
```

- **`blocks`**: "A blocks B" (`from_task=A`, `to_task=B`) → B is unclaimable while A
  is open (state not in `done`/`abandoned`). `IsBlocked(tx, taskID)` evaluates this
  inside the claim tx so the gate is checked atomically.
- **`child_of`**: "A child_of B" makes B an epic over A. Cycle detection on insert:
  `reachesViaChildOf` (BFS up the parent chain) rejects an edge that would make the
  hierarchy cyclic → `ErrCycle`. Self-edges rejected → `ErrInvalidInput`. Duplicate →
  `ErrEdgeExists`. (BFS is retained; a Postgres `WITH RECURSIVE` variant is an optional
  later optimization, not required for v1.)

### events + state_log (append-only log, provenance)

```sql
CREATE TABLE events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source      text NOT NULL CHECK (source IN ('github','flux','watcher','cli','system')),
    external_id text NOT NULL,
    type        text NOT NULL,
    payload     jsonb,
    received_at timestamptz NOT NULL,
    UNIQUE (source, external_id)
);

CREATE TABLE state_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind text NOT NULL,
    entity_id   text NOT NULL,
    change      jsonb NOT NULL,
    event_id    bigint NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    at          timestamptz NOT NULL
);
```

`RecordEvent(source, externalID, type, payload, apply)` stays the sole write entry
point and keeps its contract: insert the event `ON CONFLICT (source, external_id) DO
NOTHING`; on **first** sight call `apply(tx, eventID)` in the **same transaction**;
on a repeat, return the existing id with `inserted=false` and skip `apply`. Idempotency
by `(source, external_id)` makes redelivered webhooks, re-run watcher ticks, and
re-run sweeps safe. Every typed-table mutation (task transition, lease insert/close,
edge change) is an `apply` callback, so the append-only event and the derived-state
change commit or roll back atomically, and every change carries provenance via
`state_log.event_id → events.id`. CLI/system events with no upstream delivery id mint
a random hex `external_id` (`randomExternalID`); sweeps use the deterministic
`lease-expired-<leaseID>` so re-sweeping never double-applies.

`payload`/`change` move `TEXT` → **`jsonb`** (native, indexable); Go call sites still
marshal via `encoding/json`.

---

## Lease lifecycle

1. **Acquire** — via the claim transaction (below). Sets `acquired_at = renewed_at =
   now`, `expires_at = now + ttl`, binds `actor_id` + `worktree`.
2. **Renew** (heartbeat) — `renew(taskID, actor)` sets `renewed_at = now`, `expires_at
   = now + ttl` on the active lease **held by that actor**. A non-holder gets
   `ErrNotFound` (deliberately indistinguishable from "no lease" so a probe cannot leak
   the holder; use `ActiveLease` to inspect). An expired-but-unswept lease is still
   renewable — a returning agent can reclaim its own worktree before the sweeper acts.
3. **Release** — `release(taskID, actor)` sets `released_at = now`; if the task is
   still `in_progress`, transitions it back to `ready` (`closeLease`). If it already
   moved on (e.g. `in_review`), only the lease closes. Non-holder → `ErrNotFound`.
   Triggered by `ExitWorktree`/worktree removal (spec 05) — **the lease dies with the
   worktree.**
4. **Expiry sweep** — `ExpireLeases(now)` closes every active lease with `expires_at <
   now` and reverts each still-`in_progress` task to `ready`, one `system`
   `lease.expired` event per lease. Each expiry re-checks inside its tx (a `release`
   may have won the race) and is idempotent on `lease-expired-<leaseID>`. The serve
   loop runs it on a ticker with the store clock.

The lease is **worktree-scoped, not session-scoped**: sessions come and go against the
same worktree while the single lease persists; the worktree's removal (or expiry after
a dropped heartbeat) is what ends it.

---

## The claim transaction

One serialized transaction performs the whole pickup: **verify the task is claimable +
insert the lease + advance state**, with no read-then-write window for a racing claimer
to slip through. Signature (unchanged from today, `session_id` → `worktree`):

```
Claim(ctx, taskID, actorID, worktree, ttl) (*Lease, error)
```

Executed as a `RecordEvent("cli", <mintedID>, "lease.claimed", …)` apply callback:

1. `SELECT … FROM tasks WHERE id = $1 FOR UPDATE` — take a **row lock** on the
   candidate task. Concurrent claims of the *same* task serialize here; claims of
   *different* tasks never contend. (Unknown task → `ErrNotFound`.) This row lock is
   what replaces the old global single-writer connection.
2. Verify the actor exists (`ErrNotFound`).
3. Verify **no active lease**: `SELECT id FROM leases WHERE task_id=$1 AND released_at
   IS NULL`. Present → `ErrLeased`.
4. Verify **not blocked**: `IsBlocked(tx, taskID)` → `ErrBlocked`.
5. `Transition(tx, now, taskID, "ready", "in_progress", eventID)` — wrong state →
   `ErrBadTransition`, and the `FOR UPDATE` guarantees the read state is the write
   state.
6. `INSERT INTO leases (…) … RETURNING id`. The `leases_active` partial unique index
   is the **backstop**: any claim that beats the row lock (or a bug) still fails on
   `23505` → mapped to `ErrLeased`.

All six steps + the event insert + the `state_log` row commit atomically or not at all.

**Errors:** `ErrLeased`, `ErrBlocked`, `ErrCycle` (edge ops), plus `ErrBadTransition`
/ `ErrNotFound`. Unique-violation detection uses `pgconn.PgError.Code == "23505"` on the
`leases_active` / `task_edges` unique indexes.

**Accepting a pre-ranked candidate.** `claim --next` (spec 02) does **not** re-implement
this transaction; it *supplies the candidate id* to it. Spec 02's ranker selects the
top-ranked `ready`, unblocked, unleased task and calls `Claim` with that id. Because
`Claim` is atomic and self-verifying, a lost race surfaces as `ErrLeased`/`ErrBlocked`/
`ErrBadTransition`, and spec 02's loop simply re-ranks and retries the next candidate —
there is no list→pick→claim window to race. This spec's contract to 02: **`Claim` is a
total, atomic function of one candidate id; ranking, candidate-set construction, and
retry policy live in 02.** (Spec 02 may build its candidate scan on `FOR UPDATE SKIP
LOCKED` to skip already-contended rows; the atomic primitive here is per-candidate and
agnostic to how the candidate was chosen.)

---

## Postgres schema & data layer

The backbone is built directly on Postgres — a fresh schema baseline.

**Driver & connection.** `github.com/jackc/pgx/v5`, registered as a `database/sql` driver
via `github.com/jackc/pgx/v5/stdlib` (driver name `"pgx"`), so store functions stay
`*sql.Tx`-typed. `Open` opens a **real pool** (`SetMaxOpenConns` sized to server
concurrency, `SetMaxIdleConns`, `SetConnMaxLifetime`) against a `postgres://…` DSN.
Deterministic clock injection (`nowFn` / `SetNowFunc`) is retained (determinism lens).

**Migrations (golang-migrate).** `database/postgres` driver; source `file://` or embedded
via `iofs` (use the `golang-migrate` skills for authoring/lint/round-trip). The baseline
schema uses `bigint GENERATED ALWAYS AS IDENTITY` keys, `timestamptz` timestamps,
`boolean` flags (`projects.deploy_gated`, `actors.admin`), `jsonb` for `payload`/`change`,
a `leases.worktree` column with a `leases_active_worktree` partial unique index, and
partial unique indexes + `CHECK`s. FKs are enforced by default.

**Transaction semantics.** Default isolation is **READ COMMITTED**; claim correctness
relies on the explicit `SELECT … FOR UPDATE` row lock (step 1) plus the `leases_active`
unique-index backstop, **not** on `SERIALIZABLE`. SERIALIZABLE was rejected: it would
force application-level retry loops for no benefit here, since the contended resource (one
task's lease) is already pinpointed by the row lock. *(Open Q: confirm READ COMMITTED +
`FOR UPDATE`.)*

**Deployment.** Postgres runs as a CNPG cluster in-cluster (see the Sunstone `kubernetes`
skill); migrations run as a golang-migrate init-container/Job (see the
`golang-migrate:k8s-job` skill) before the server starts. The `serve` sweep loop and
migrations must be safe under **multiple server replicas** — event idempotency covers
double-sweeps; if a single sweeper is wanted, gate it with a Postgres advisory lock.
*(Open Q.)*

---

## CLI / API surface touched

- `POST /api/v1/tasks/{id}/claim` — request field `session_id` → **`worktree`**
  (`ttl` unchanged); response `Lease.session_id` → `Lease.worktree`.
- `POST /api/v1/tasks/{id}/renew`, `POST /api/v1/tasks/{id}/release` — unchanged shape;
  holder identity is `(actor, worktree)`.
- `lode task claim|renew|release` — a `--worktree` value replaces the
  session flag; hooks (spec 05) supply it. `--json` output carries `worktree`.
- **New reopen path** — `lode task reopen <id>` → `done|abandoned → ready`
  (`task.reopened` event).
- `claim --next` endpoint/flags (`--project`, `--strict-focus`) are **defined in spec
  02**; this spec only guarantees the atomic `Claim` primitive it calls.

No MCP (D14, Q14.1) — agents drive `lode --json`; no per-tool schema tokens in context.

---

## Dependencies

- **Upstream:** none — this is the foundation spec. External: pgx v5, golang-migrate
  Postgres driver, a running Postgres (CNPG). Sunstone skills:
  `kubernetes` (CNPG), `golang-migrate:*` (author/lint/k8s-job/round-trip).
- **Downstream (depend on 01):** 02 (ranking calls `Claim`; `concern`/`focus` columns
  extend `tasks`), 03 (projects Task/edges/events into the graph via IRI), 05 (hooks
  drive claim/renew/release and supply `worktree`).

---

## Open questions

1. ~~Reopen target~~ — **RESOLVED: `done → ready`** (forces a fresh claim; keeps the
   invariant "no `in_progress` without a live lease").
2. **Worktree identity canonical form** — `<host>:<abs-path>` vs git worktree UUID vs
   the deterministic `wt/<id>-<slug>` name. Cross-host reclaim implications. The backbone
   treats it opaque; exact form is fixed in spec 05.
3. **Isolation level** — confirm READ COMMITTED + `FOR UPDATE` + unique-index backstop
   (vs SERIALIZABLE).
4. **Sweeper under multiple replicas** — rely on event idempotency alone, or gate the
   sweep with a Postgres advisory lock to elect a single sweeper?
5. **Task ↔ GitHub Issue mirror** *(new; from 04 review).* A backbone Task mirrors bidirectionally
   to a GitHub Issue so PRs can join it the native way (`Closes #N`). The backbone owns the Task and
   the mirror link (projected to the graph as `ls:mirrors`, spec 03); the plugin (05) creates and
   syncs the Issue. Open: create-on-task-create vs. lazy; who wins on divergent edits
   (backbone-authoritative?); handling tasks with no mirror yet.

---

## Acceptance criteria

1. `store` runs on Postgres via pgx (`database/sql`/`stdlib`) with a real connection pool
   (no single-writer cap). The full `internal/store` test suite passes against an
   ephemeral Postgres.
2. A golang-migrate Postgres baseline creates all backbone tables with `timestamptz`,
   identity PKs, `jsonb` payloads, `boolean` flags, the `leases_active` **and**
   `leases_active_worktree` partial unique indexes; `migrate up`/`down` round-trips
   clean (`golang-migrate:test-roundtrip`).
3. Leases are worktree-bound: `claim`/`renew`/`release` operate on `(actor, worktree)`;
   no `session_id` remains in schema, API, or CLI. A worktree holds at most one active
   lease; a task has at most one active lease.
4. The claim transaction is atomic and race-safe: under concurrent claims of one task,
   exactly one wins and the losers get `ErrLeased` — verified by a concurrency test
   that fires N parallel `Claim`s at a single `ready` task. Blocked/unblocked and
   non-`ready` cases return `ErrBlocked` / `ErrBadTransition`.
5. `Claim` accepts a caller-supplied candidate id and returns typed errors on loss,
   with **no ranking logic** in the backbone (ranking is spec 02).
6. State machine enforces the table above including reopen; `done→ready` and
   `abandoned→ready` produce a `task.reopened` event and `state_log` row; every illegal
   transition is `ErrBadTransition`.
7. Every task/lease/edge mutation commits atomically with its `events` row and carries
   provenance via `state_log.event_id`; `RecordEvent` idempotency holds (redelivered id
   does not double-apply); the expiry sweep is idempotent and reverts expired
   in-progress tasks to `ready`.
8. `blocks` gates claimability and `child_of` cycle detection rejects cyclic edges
   (`ErrCycle`) — both verified atomically inside the claim/edge transactions.
