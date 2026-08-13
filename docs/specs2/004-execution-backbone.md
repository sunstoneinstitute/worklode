---
status: draft
---
# Spec 004 — Execution backbone

## 0. Purpose & scope {#sec-0}

The execution backbone is the ACID core Worklode's pickup loop turns on: task state,
worktree-bound leases, the append-only event log, and the two edge types
(`blocks`, `child_of`) that gate what is claimable. It runs on **Postgres** (D2), with
the lease bound to **git-worktree identity** (D8/D11/D14).

**Concurrency.** Postgres lets N `claim --next` calls proceed concurrently, serializing
only on the specific task row(s) they actually contend for — the write throughput D8
requires for 24/7 parallel agents.

**In scope:** the Postgres schema baseline, row-lock transaction semantics, the task
state machine (incl. reopen and delivery), worktree-bound lease lifecycle, the atomic
`claim` transaction (the transaction only — not the ranking), the event log +
provenance, per-project task keys, task hierarchy, and the two gating edges with cycle
detection.

**Out of scope (reference by title, do not duplicate):**
`claim --next` **ranking**, `concern`, `focus`, `--strict-focus`, `needs-decomposition`
→ **spec 005 (Prioritization & pickup)**. RDF vocabulary, IRI scheme, backbone→graph
projection → **spec 006 (Knowledge graph)**. Observed-layer derivers and drift queries
→ **spec 007 (Drift & overview)**. Hooks, slash commands, worktree naming/creation,
auto-resume → **spec 008 (Worklode plugin)**. The observed/projection tables in the same
database (`issues`, `pull_requests`, `ci_runs`, `reviews`, `artifacts`, `deployments`,
`runtime_events`) have their **semantics** owned by 006/007; this spec only guarantees
they exist in the Postgres schema.

## 1. Data model {#sec-1}

Target Postgres schema for the backbone tables this spec owns. Conventions:
**`timestamptz`** timestamps; **`bigint GENERATED ALWAYS AS IDENTITY`** keys; **`boolean`**
flags; partial unique indexes and `CHECK` constraints.

### 1.1 tasks {#sec-1.1}

Task ids are `<PROJECT-KEY>-<n>`, drawn from a per-project `projects.next_task_num`
counter (migration `0003_project_keys`); §2 is the subsystem. The retired identity was
a global `WT-<n>` allocated from a single-row `task_seq` counter — `UPDATE task_seq SET
next = next + 1 WHERE id = 1 RETURNING next - 1`, which preserved gapless allocation
where a bare `SEQUENCE` would gap on rollback and complicate the `WT-` prefix.
`task_seq` is dropped.

```sql
CREATE TABLE tasks (
    id         text PRIMARY KEY,                    -- <KEY>-<n>
    project_id text NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    title      text NOT NULL,
    body       text,
    priority   text NOT NULL CHECK (priority IN ('critical','high','medium','low')),
    kind       text NOT NULL CHECK (kind IN
                 ('feature','bug','chore','design','review','spike')),
    state      text NOT NULL CHECK (state IN
                 ('draft','ready','in_progress','in_review','merged',
                  'deployed_dev','deployed_prod','released','abandoned')),
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
```

The kind enum matches `wlc:TaskKind` (014 §8). `review` and `spike` arrived with
`0009_task_kinds`; `spec` is renamed `design` (025 §6); and `epic`, added by
migration `0006` (§6.2), is removed again (029 §2) — §6 states what carries the
container role instead. The `state` CHECK covers the delivery states of §5.1.

### 1.2 leases, bound to the worktree {#sec-1.2}

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
never parses it. Recommended canonical form `<host>:<abs-worktree-root>`; a
deterministic worktree path makes `/lode-resume`'s lookup trivial (D14). That path is
`<worktree_dir>/<branch>` (default `.worktrees/<id>-<slug>`), retiring the
`wt/<id>-<slug>` spelling. Exact composition is fixed in **spec 008**
(`008-worklode-plugin.md`); the backbone only requires it be stable for a
worktree's lifetime and unique per live worktree. *(Open Q2.)*

`DefaultLeaseTTL = 2h` (unchanged). Renewal is a **commit-cadence heartbeat**
(D8): the `PreToolUse`/git `pre-commit` hook calls `renew` before each commit-batch —
no wall-clock timer. Missing the heartbeat (session died, machine slept) lets the
lease lapse; the sweeper reclaims it.

### 1.3 task_edges {#sec-1.3}

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
  is open. A is open until it reaches its repo mapping's `done_state` — the state
  that counts as delivered for that repo (§5.1) — or a later state on that repo's
  delivery path, or is `abandoned`. **The closed set is per repo, not one fixed list
  of states**: a task at `merged` is closed where `done_state = merged` and still
  open where the repo gates on `released`. The one exception is a task with
  children, which has no commit of its own and so cannot advance past `merged`
  (§6.4); it is closed at `merged` in every repo. `closedStates` (`tasks.go:537`)
  is therefore a predicate joined through the repo mapping rather than a constant
  state tuple. `IsBlocked(tx, taskID)` evaluates it inside the claim tx so the gate
  is checked atomically.
- **`child_of`**: "A child_of B" makes B the parent of A; §6 governs what parent-hood
  means. Cycle detection on insert: `reachesViaChildOf` (BFS up the parent chain)
  rejects an edge that would make the hierarchy cyclic → `ErrCycle`. Self-edges
  rejected → `ErrInvalidInput`. Duplicate → `ErrEdgeExists`. (BFS is retained; a
  Postgres `WITH RECURSIVE` variant is an optional later optimization, not required
  for v1.)

### 1.4 events + state_log — append-only log, provenance {#sec-1.4}

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
point and keeps its contract: insert the event
`ON CONFLICT (source, external_id) DO NOTHING`; on **first** sight call
`apply(tx, eventID)` in the **same transaction**; on a repeat, return the existing id
with `inserted=false` and skip `apply`. Idempotency by `(source, external_id)` makes
redelivered webhooks, re-run watcher ticks, and re-run sweeps safe. Every typed-table
mutation (task transition, lease insert/close, edge change) is an `apply` callback, so
the append-only event and the derived-state change commit or roll back atomically, and
every change carries provenance via `state_log.event_id → events.id`. CLI/system
events with no upstream delivery id mint a random hex `external_id`
(`randomExternalID`); sweeps use the deterministic `lease-expired-<leaseID>` so
re-sweeping never double-applies.

`payload`/`change` move `TEXT` → **`jsonb`** (native, indexable); Go call sites still
marshal via `encoding/json`.

## 2. Per-project task keys {#sec-2}

Task IDs are `WL-<n>` for every project: the prefix is the literal `"WL-"`
(`internal/store/tasks.go:93`) and `<n>` comes from a single global counter
(`task_seq`, one row — `0001_baseline.up.sql:62`). A second project would get
`WL-12`, not its own code counting from 1. We want Jira-style per-project codes:
`WL-1…` for worklode, `SW-1…` for the next project.

### 2.1 Decisions {#sec-2.1}

| Decision | Choice |
|---|---|
| Code (`key`) | Required on project creation, unique, uppercase, immutable |
| Key format | `^[A-Z][A-Z0-9]{1,9}$` (letter first, 2–10 chars) |
| Numbering | Per-project counter, starting at 1 |
| Global `task_seq` | Dropped |
| Existing `WL-1…11` | Preserved; worklode's key backfilled to `WL`, counter to 12 |
| ID format | `<KEY>-<n>` (e.g. `WL-12`, `SW-1`) |

Immutable because the key is baked into permanent task IDs, branch names (then
spelled `wl/<id>`, since retired — §2.5), and `WL-Task:` PR markers — changing it
would orphan those references.

### 2.2 Data model — migration `0003_project_keys` {#sec-2.2}

```sql
ALTER TABLE projects ADD COLUMN key text;
ALTER TABLE projects ADD COLUMN next_task_num bigint NOT NULL DEFAULT 1;

-- Backfill from existing task-id prefixes (data-driven, not hardcoded):
-- worklode -> key 'WL', next_task_num max(n)+1 = 12.
UPDATE projects p SET key = s.prefix, next_task_num = s.maxnum + 1
FROM (SELECT project_id,
             split_part(id, '-', 1)               AS prefix,
             max(split_part(id, '-', 2)::bigint)   AS maxnum
      FROM tasks GROUP BY project_id, split_part(id, '-', 1)) s
WHERE p.id = s.project_id;

-- Fallback for projects with no tasks yet (none today): derive from id.
UPDATE projects
SET key = upper(substr(regexp_replace(id, '[^a-zA-Z0-9]', '', 'g'), 1, 4))
WHERE key IS NULL;

ALTER TABLE projects ALTER COLUMN key SET NOT NULL;
ALTER TABLE projects ADD CONSTRAINT projects_key_unique UNIQUE (key);
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$' AND key NOT IN ('SPEC','ADR'));

DROP TABLE task_seq;
```

`SPEC` and `ADR` are reserved and rejected as project keys (014 §11.3): they are the
type token of the `<PROJECTKEY>-<TYPE>-<n>` document shorthand, and a project holding
one would make a reference ambiguous — that is the `AND key NOT IN ('SPEC','ADR')`
clause on the format CHECK above.

The `.down.sql` recreates `task_seq` (seeded from `max(next_task_num)`), drops
the two columns, and reverses the constraints.

### 2.3 ID generation {#sec-2.3}

`CreateTask` replaces the global-sequence read with a per-project one, in the
same transaction:

```sql
UPDATE projects SET next_task_num = next_task_num + 1
WHERE id = $1 RETURNING key, next_task_num - 1
```

then `id := fmt.Sprintf("%s-%d", key, n)`. IDs stay globally unique (unique key
× per-project number). A missing project row surfaces the existing
`ErrInvalidInput`/not-found path.

### 2.4 API and CLI {#sec-2.4}

- `store.CreateProject(ctx, id, name, key)` — new `key` param
  (`internal/store/projects.go:35`).
- `POST /api/v1/projects` — `key` required; validate format server-side, map a
  unique-violation to a 400 "project key already in use" rather than a 500
  (`internal/api/admin.go:52`). `projectJSON` gains `key`
  (`admin.go:23`, `listProjects` at `admin.go:81`).
- `lode project add <id> --name … --key WL` — new required `--key` flag
  (`internal/cmd/project.go:38`); `project list` gains a `KEY` column.

### 2.5 Task-key parsing and branch names {#sec-2.5}

Task keys MUST conform to `^[A-Z]+-\d+$`. Every parser that used to hardcode the
literal `WL-` matches `[A-Z][A-Z0-9]*-\d+` instead:

- `internal/worktree/worktree.go:18` — `dirRe` for `wt/<id>[-slug]`.
- `internal/store/changes.go:51` — `refTaskIDPattern` for task branches.
- `internal/store/changes.go:67` — `bodyTaskIDPattern`: keep the literal
  `WL-Task:` marker label (a fixed convention, not the id prefix); generalize
  only the captured id.
- `internal/store/ranking.go:167` — `numericTaskID`: parse the digits after the
  last `-` (`id[strings.LastIndex(id,"-")+1:]`) instead of `TrimPrefix(id,"WL-")`.

Branch names are rendered by the server from `LODE_BRANCH_TEMPLATE` (default
`{{ .id }}-{{ .slug }}`), and `store.TaskIDFromRef`'s pattern is derived from that
template rather than from a fixed prefix regex (030 §2).
`008-worklode-plugin.md` is authoritative on the template grammar and on worktree
naming. Three spellings are retired and no longer recognized: the configurable
`LODE_BRANCH_PREFIX` with its `lode/` default, the hardcoded `wl/` prefix it had
replaced, and the `<prefix><task-key>[-slug]` branch forms they produced
(`wl/<id>-<slug>`, e.g. `wl/SW-3-…`).

ADRs and specs are additionally addressable through a `<PROJECTKEY>-{ADR,SPEC}-<n>`
alias (e.g. `WL-ADR-1`, `WL-SPEC-14`), with `<n>` taken from the document's own
filename number rather than from the task sequence — the shorthand exists so a
reference is typeable without a lookup (014 §11.3).

## 3. Lease lifecycle {#sec-3}

1. **Acquire** — via the claim transaction (below). Sets
   `acquired_at = renewed_at = now`, `expires_at = now + ttl`, binds `actor_id` +
   `worktree`.
2. **Renew** (heartbeat) — `renew(taskID, actor)` sets `renewed_at = now`,
   `expires_at = now + ttl` on the active lease **held by that actor**. A non-holder
   gets `ErrNotFound` (deliberately indistinguishable from "no lease" so a probe
   cannot leak the holder; use `ActiveLease` to inspect). An expired-but-unswept lease
   is still renewable — a returning agent can reclaim its own worktree before the
   sweeper acts.
3. **Release** — `release(taskID, actor)` sets `released_at = now`; if the task is
   still `in_progress`, transitions it back to `ready` (`closeLease`). If it already
   moved on (e.g. `in_review`), only the lease closes. Non-holder → `ErrNotFound`.
   Triggered by `ExitWorktree`/worktree removal (spec 008) — **the lease dies with the
   worktree.**
4. **Expiry sweep** — `ExpireLeases(now)` closes every active lease with
   `expires_at < now` and reverts each still-`in_progress` task to `ready`, one
   `system` `lease.expired` event per lease. Each expiry re-checks inside its tx (a
   `release` may have won the race) and is idempotent on `lease-expired-<leaseID>`.
   The serve loop runs it on a ticker with the store clock.

Closing a lease — release, expiry sweep, or completion — also stamps `ended_at` on
every open `agent_sessions` row for that lease, in the same transaction and without
an event of its own (spec 012).

The lease is **worktree-scoped, not session-scoped**: sessions come and go against the
same worktree while the single lease persists; the worktree's removal (or expiry after
a dropped heartbeat) is what ends it.

## 4. The claim transaction {#sec-4}

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
3. Verify **no active lease**:
   `SELECT id FROM leases WHERE task_id=$1 AND released_at IS NULL`. Present →
   `ErrLeased`.
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

**Accepting a pre-ranked candidate.** `claim --next` (spec 005) does **not** re-implement
this transaction; it *supplies the candidate id* to it. Spec 005's ranker selects the
top-ranked `ready`, unblocked, unleased task and calls `Claim` with that id. Because
`Claim` is atomic and self-verifying, a lost race surfaces as `ErrLeased`/`ErrBlocked`/
`ErrBadTransition`, and spec 005's loop simply re-ranks and retries the next candidate —
there is no list→pick→claim window to race. This spec's contract to 005: **`Claim` is a
total, atomic function of one candidate id; ranking, candidate-set construction, and
retry policy live in 005.** (Spec 005 may build its candidate scan on `FOR UPDATE SKIP
LOCKED` to skip already-contended rows; the atomic primitive here is per-candidate and
agnostic to how the candidate was chosen.)

## 5. Delivery lifecycle {#sec-5}

The task state machine ends at `merged`, reached only from `in_review` (PR-merge
auto-transition or manual `lode task done`). This misses two realities:

1. Work sometimes lands on `main` without a PR; nothing detects it.
2. "Merged" is not "delivered". A service (data-platform, worklode) is delivered
   when it runs in prod, via two stages (dev, then prod). A library
   (sunstone-py) is delivered when a release is published. Today nothing
   tracks either; the per-project `deploy_gated` flag only *blocks* the
   merge→merged transition and nothing ever unblocks it.

The goal is one generic, event-driven mechanism for all sunstoneinstitute
repos — no per-repo lifecycle configuration.

### 5.1 State machine {#sec-5.1}

```
draft → ready → in_progress → in_review → merged → deployed_dev → deployed_prod
                     │                     ↑ │  ↘ released        (terminal)
                     └─────────────────────┘ └──── (terminal, release-based repos)
                        direct-to-main jump
```

New states: `deployed_dev`, `deployed_prod`, `released`.

| State | Meaning |
|---|---|
| `merged` | Work landed on the default branch (auto-detected or manual `lode task done`) |
| `deployed_dev` | Deploy to a dev/test environment covering the landed commit: GitHub `deployment_status: success` **and** Flux `ReconciliationSucceeded` |
| `deployed_prod` | Same, for prod |
| `released` | GitHub release published covering the landed commit |

Each repo mapping carries a **`done_state`** — the terminal state that counts
as fully delivered for that repo: `merged`, `deployed_prod`, or `released`.
Discovery defaults it (prod env → `deployed_prod`; releases without a prod
env → `released`; neither → `merged`); it can be set explicitly on the repo
mapping. A task at its repo's `done_state` is fully delivered; states past it
are never expected for that repo.

`done_state` also selects which delivery branch the resolver walks. A repo
with `done_state = released` follows `merged → deployed_dev → released` and
ignores prod deploys — `deployed_prod → released` is deliberately not a legal
transition, so advancing on a prod deploy would strand such a repo's tasks one
hop short of delivered, permanently. Every other repo follows `merged →
deployed_dev → deployed_prod` and ignores releases. The asymmetry for
`done_state = merged` is deliberate: those tasks still advance past `merged`
when deploy facts exist, because `merged` is also the default for repos
discovery has not profiled yet, and real deploy signals outrank a default.

The base transitions and their triggers:

| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | publish (make claimable) |
| `ready` | `in_progress` | **claim** (lease acquired) |
| `in_progress` | `in_review` | submit for review |
| `in_progress` | `ready` | **release / lease expiry** |
| `in_review` | `merged` | accept |
| `in_review` | `in_progress` | rework |
| `draft`·`ready`·`in_progress`·`in_review` | `abandoned` | abandon |

`Transition(tx, now, taskID, from, to, eventID)` stays the guard: it verifies the
membership in the transition set **and** that the task's current state equals
`from`, atomically inside the caller's tx, then bumps `updated_at` and appends a
`state_log` row attributed to `eventID`. Unknown task → `ErrNotFound`; wrong
from-state → `ErrBadTransition`.

New legal transitions:

- `ready|in_progress|in_review → merged` — landing on main advances the task
  from wherever it sat; the resolver never advances a `draft`.
- `merged → deployed_dev | deployed_prod | released` — skipping `deployed_dev`
  is legal (prod-only repos, or a missed dev signal).
- `deployed_dev → deployed_prod | released`.
- Reopen: `deployed_dev|deployed_prod|released → ready`, same as `merged → ready`.
  Reopening also clears the task's `task_commits` in the same transaction, so
  the next webhook cannot resolve the task straight back to the delivery state
  it was reopened out of. Delivery is re-earned by the new work landing.
- `abandoned` stays reachable only from pre-`merged` states.

All delivery transitions are forward-only; the resolver never walks a task
backward, and none of them closes the lease. The lease is worktree-scoped
(004 §2) and ends only on `release`, `abandon`, `reopen`, or the expiry sweep.
Closing it on merge would serve neither purpose a lease has: mutual exclusion is
already enforced by `Claim` requiring state `ready`, so a `merged` task is
unclaimable regardless of its lease, and liveness recovery is already the 2h TTL
expiry sweep. Delivery state is a fact about the code; the lease is a fact about
who occupies the worktree — and work legitimately continues in a worktree after
its branch is deployed to dev. Whether manual `lode task done` also stops closing
the lease is undecided and not addressed here.

Environment-name normalization: `dev`, `test`, `development`, `staging` → dev
stage; `prod`, `production` → prod stage; everything else (`copilot`,
`github-pages`, `pypi`, `*-apply`, …) is ignored. This normalization applies
to GitHub environment names only: `LODE_CLUSTER_ENV_MAP` (cluster → stage, for
Flux events) is operator config and is validated at startup to contain nothing
but `dev` and `prod`, since any other value would record `deployments` rows
that can never advance a task.

`projects.deploy_gated` is retired; this mechanism replaces it.

### 5.2 Fact tables {#sec-5.2}

Four tables, written inside the same `RecordEvent` transaction as the
webhook that produced them:

**`task_commits`** — `(task_id, repo, sha, source, seen_at)`. Attributes
commits to tasks. Sources:

- pushes to task branches. The branch pattern is derived from
  `LODE_BRANCH_TEMPLATE` (default `{{ .id }}-{{ .slug }}`; `008-worklode-plugin.md`
  is authoritative, and §2.5 restates it) rather than from a fixed prefix; the
  retired `wl/` prefix is no longer recognized.
- PR correlation (existing head-ref / `WL-Task:` body mechanisms; the PR's
  SHAs join this table).
- task-key references in commit messages on default-branch pushes (fallback
  for commits made directly on main).

**`main_commits`** — `(repo, sha, seq, pushed_at)`. Every default-branch push
appends its commits in order. Main history is linear in push order, so
"commit X is included in the state at commit Y" is `seq(X) <= seq(Y)` — no
git ancestry calls, no clone. A task's **landed seq** is the seq of the main
commit that matched it (a `task_commits` SHA in the push, a merge-commit
message naming the task branch, or a task-key marker in a commit message).

**`env_deploys`** —
`(repo, environment, main_seq, gh_status, flux_status, updated_at)`, environment
normalized to `dev`/`prod`. The per-environment **deployed frontier**:

- GitHub `deployment_status: success` sets `gh_status`. A deployment SHA on
  main resolves to its seq directly; a `last-deploy/*` SHA resolves via the
  `main-sha:` trailers in the cherry-picked commits (visible in the push
  payload for that branch).
- A Flux `ReconciliationSucceeded` whose revision SHA matches a known
  deploy-branch or main SHA for the repo sets `flux_status` (environment via
  the existing cluster→env mapping). Flux failures mark the attempt failed,
  surfacing reconciliation failures.

A frontier is confirmed at seq N only when both signals are present. Every
task with landed seq ≤ N is covered — one integer comparison handles batched
deploys carrying many tasks.

Dual-signal gating requires that Flux events for the repo/env are actually
correlatable (revision SHAs matching the repo's branches). For a repo/env
where no Flux revision has ever matched — a deploy not reconciled by Flux, or
a cluster whose webhook isn't wired yet — the GitHub signal alone confirms
the frontier; the first matching Flux revision upgrades that repo/env to
dual-signal gating permanently. This prevents tasks stranding at `merged` while
still enforcing Flux confirmation everywhere it exists.

The `flux_seen` latch is permanent and has no un-latch path: once a Flux
revision has correlated for a repo/env, that pair requires both signals
forever, so a repo whose Flux wiring is later removed strands its tasks at
`merged`. A revision that correlates to the wrong repo (shared history between
tracked repos) latches a pair onto a signal that will never arrive. The
handler logs `flux delivery gating latched` (repo, environment, revision) on
the transition — that line is the operator's only trace when diagnosing tasks
stuck at `merged`; the fix is a `flux_seen` reset in the database.

**`release_frontiers`** — `(repo, tag, main_seq, published_at)`. A
`release.published` event records the seq the tag covers; tasks at or below it
count as released. The frontier is the release's `target_commitish` when that
resolves to a known main commit, so a backport tag covers only what it
contains; `target_commitish` is often a branch name (UI-created tags), which
does not resolve, and the frontier then falls back to main's head as of the
webhook's arrival — right for release-on-merge. Forward-only per tag.

### 5.3 Handlers and resolver {#sec-5.3}

All handlers use the existing HMAC/idempotency plumbing in `internal/hooks`.

- **`push`** (new): by ref — a task branch (the retired prefix form was
  `<prefix>*`) → insert `task_commits`; default
  branch → append `main_commits`, set landed seqs; `last-deploy/<env>` →
  record deploy-branch-SHA → main-seq mapping from `main-sha:` trailers.
- **`deployment_status`** (new): normalize environment, resolve deployment
  SHA to a main seq, upsert `env_deploys.gh_status`. A SHA that is not yet
  known on main is dropped in v1 (no pending-facts store); the next deploy of
  that repo re-establishes a frontier that covers it.
- **Flux handler** (extended): also resolve revision SHA to `(repo, main seq)`
  and update `env_deploys.flux_status`. Existing `deployments`-table
  behavior unchanged.
- **`pull_request`** (changed): merged-PR handling records facts only; the
  `in_review → merged` transition moves into the resolver. The `deploy_gated`
  branch is deleted.
- **`release`** (extended): still creates the artifact; also records the
  release frontier.

**Resolver** — `ResolveDelivery(tx, taskID)`: reads the task's landed seq,
env frontiers, and release frontier; computes the furthest supported
milestone; issues forward-only transitions (multi-step in one resolve when
signals arrived out of order). Every handler calls it for affected tasks at
the end of its apply. All lifecycle rules live here; handlers only record
facts. Arrival order of GitHub and Flux events therefore never matters.

**Repo delivery profile**: on `project add-repo`, fetch the repo's environment
list via the GitHub App (`internal/githubauth`) and note whether it uses
releases. This seeds the repo's `done_state`, which stays explicitly settable
with `lode project set-repo --done-state` (v1 has no lazy re-discovery from
webhook traffic). Discovery never gates transitions: if it fails, the repo
keeps `done_state = merged` and states still advance from events alone.

GitHub App requirements: repository permissions **Actions: read**
(environment discovery, `GET /repos/{owner}/{repo}/environments`),
**Deployments: read** (`deployment_status` webhook events), **Contents:
read** (`push` and `release` webhook events, and `GET
/repos/{owner}/{repo}/releases/latest` for discovery); webhook subscriptions
for `push` and `deployment_status` added alongside the existing events.
Without Actions: read the environments call 403s, discovery fails, and every
repo keeps the default `done_state = merged`, so tasks stop advancing at
`merged`. The only trace is the server's `discover repo done_state` warn log
at `add-repo` time; the repair is `lode project set-repo <repo> --done-state`.

Deferred: some repos deliver multiple artifacts (data-platform ships two
docker images plus a python library; worklode a docker image plus a CLI
binary via brew tap). v1 models one `done_state` per repo; per-artifact
delivery tracking is future work.

**Deployment config** (not code): Flux notification-controller in every
cluster gets a Provider/Alert pointing at worklode's `/hooks/flux`.

### 5.4 Surface changes {#sec-5.4}

- New states flow through existing surfaces: task JSON, `lode task list`
  filters, web UI badges.
- Task timeline shows delivery facts: landed on main at `<sha>`, dev deploy
  confirmed, prod deploy confirmed, released in `<tag>`.
- `lode task done` remains the manual escape hatch.
- `lode project add-repo` output gains the discovered delivery profile.
- The claim and claim-next API responses carry a server-derived `branch`,
  rendered from `LODE_BRANCH_TEMPLATE` (default `{{ .id }}-{{ .slug }}`;
  `008-worklode-plugin.md` is authoritative), so branch naming is decided in one
  place; against a server too old to send one the CLI falls back to the bare
  `<id>-<slug>` (the retired fallback was `lode/<id>-<slug>`).

Repos shared across projects (`provisioning`, `admin-cluster`,
`rdf-registry`, …) need no special handling: delivery advances a task via the
repo its own commits landed in (`task_commits`), never by fan-out through
project→repo links. A delivery in a shared repo affects exactly the tasks
correlated to it; cross-project impact is modeled as multiple linked tasks.

Deferred: a single task whose work spans several repos (e.g. adding a new
application touches the app repo plus `admin-cluster` or `provisioning`)
still tracks delivery only through its primary repo in v1. Multi-repo task
delivery — possibly spotted by watching Flux events for the companion repos —
is future work.

### 5.5 Migration {#sec-5.5}

One schema version: create `task_commits`, `main_commits`, `env_deploys`,
`release_frontiers`; extend the `tasks.state` CHECK constraint; drop
`projects.deploy_gated`. Existing `merged` tasks stay `merged` — no backfill.

### 5.6 Error handling {#sec-5.6}

- A failed correlation never fails a delivery (existing principle).
- An unresolvable SHA is dropped, not parked — v1 has no pending-facts store.
  A `deployment_status` for a SHA with no known main commit records nothing
  and self-heals on the repo's next deploy, whose frontier covers it too. A
  Flux revision that correlates to no repo records nothing and does not latch.
  A release whose `target_commitish` does not resolve falls back to main's
  head instead of dropping.
- Idempotent under redelivery: facts are natural-key upserts; transitions are
  forward-only.

### 5.7 Known limitations {#sec-5.7}

- **Push payloads carry at most 2048 commits.** A larger push drops the rest
  from `main_commits`, so a task whose landing commit falls outside that
  window is never attributed and strands at `in_review`. Those commits are
  recoverable only by reconciliation (spec 013); fetching the full range
  through the App is not worth its cost at this cap.

  Truncation is at least never silent. There is no truncation flag on the
  payload, but the `commits` array is every commit between `before` and
  `after`, so an `after` absent from it proves the delivery was partial.
  That case increments `worklode_webhook_push_truncated_total` and logs the
  repo, ref and sha range. The delivery still applies the commits it did
  carry — dropping it would lose those too.
- **Pushes over 25 MB of payload are not delivered at all.** GitHub drops the
  webhook rather than truncating it, so nothing arrives to detect and the
  push is invisible until reconciliation.
- **The `flux_seen` latch never releases** (see `env_deploys` above):
  a repo/env that once correlated a Flux revision requires both signals
  forever.
- **Discovery runs only at `add-repo`.** A repo that later gains a prod
  environment or starts cutting releases keeps its old `done_state` until
  `lode project set-repo --done-state`.

## 6. Task hierarchy & decomposition {#sec-6}

`kind = 'epic'` is dropped, not renamed: a plan is a document, not a task, and
accepting it mints its tasks directly — grouped by their reference to the plan
document, with no root row above them (025 §5). What survives here is the
`child_of` machinery, narrowed to decomposing an oversized task (§6.10), which
creates parent-hood and its children together now that creating a container outright
and converting one in place are both retired. Every mechanism below (ready-set exclusion, restricted
state machine, roll-up, depth cap, single parent, brief) applies to *a task that
has children*, with no kind to declare: the retired design chose declared over
inferred because a container had to exist before its children did (§6.1), and with
`decompose` creating parent-hood and children in one transaction, "has children" is
exactly as sharp as a column. `decompose` no longer converts the parent's kind, and
`checkHierarchy` accepts an ordinary task as parent; what the epic was built *for*
is carried by the project and the milestone, both real objects with facts of their
own (029 §2).

The `child_of` machinery is half-built. The edge type exists and is
cycle-checked, the HTTP API accepts it, and the web task page renders
Parent/Children — but nothing writes it and no decision the system makes consults
it.

| Layer | `child_of` today |
|---|---|
| Schema | `task_edges.type IN ('child_of','blocks')`, `UNIQUE (from_task,to_task,type)` (`0001_baseline.up.sql:65`) |
| Store | `AddEdge` (`tasks.go:401`) cycle-checks child→parent via `reachesViaChildOf` (`tasks.go:443`) |
| API | `POST`/`DELETE /api/v1/tasks/{id}/edges` (`server.go:267`) |
| Web | Parent and Children on the task page (`web.go:167`, `:175`) |
| CLI | reads it (`lode task show` prints edges) — **nothing writes it** |
| Ranking | ignored: `readyCandidates` (`ranking.go:61`) filters on `blocks` only |
| Roll-up | none |

Two consequences. A long plan cannot be represented as a tracking task plus its
phases, so decomposition output arrives as a flat list of unrelated tasks. And
`needs_decomposition` (spec 005) is a dead-end flag: it removes an oversized
task from the pickup loop with no supported way to split it.

### 6.1 Decisions {#sec-6.1}

Taken here with rationale.

| Decision | Choice |
|---|---|
| Container identity | Inferred: a task that has `child_of` children |
| Parents per task | Exactly one (partial unique index) |
| Hierarchy depth | Max 2 edges, now spanning task → subtask only |
| Parent claimable | Never — excluded from the ready set |
| Parent delivery states | Forbidden: `in_review`, `deployed_dev`, `deployed_prod`, `released` |
| Parent closure | Automatic roll-up, forward and backward |
| Progress | Derived on read, never stored |
| Cross-project children | Rejected in v1 |
| Child ordering | Out of scope |
| Blocker inheritance | Out of scope — hierarchy and blocking stay orthogonal |
| Parent kind | Any ordinary task; `checkHierarchy` requires no declared kind |
| Direct claim of a parent | Rejected in `Claim` as well as excluded from the ready set |

**Why inferred rather than declared:** the retired design declared the container
with `kind = 'epic'`, on the reasoning that inference means one `AddEdge` call
silently changes whether a task can be claimed and what a live lease on it means,
while declaring makes conversion an explicit act that can validate its
preconditions and turns the ready-set exclusion into a column predicate. With
`decompose` creating parent-hood and its children in one transaction there is no
window in which a container exists without children, so "has children" is exactly
as sharp as a column, and 029 §2 leaves no kind to declare.

**Why a parent still needs its own guard:** `ready -> in_progress` is a legal
transition for a task with children — it is the roll-up trigger — so excluding
such tasks from the ready set alone would still let `lode task claim
<parent-id>` through; `Claim` carries the same guard. `checkHierarchy` accepts an
ordinary task as parent (029 §2); the retired rule required the parent to already
be `kind = 'epic'` and `AddEdge` rejected any other parent (422), with two
supported ways to get one — create it (`lode task add --kind epic`) or convert in
place (`lode task decompose`). There is no `lode task edit --kind`.

**Why a depth cap:** the brief is a bounded-payload contract (`brief.go:9-19`)
and the tree walks that feed roll-up and breadcrumbs are unbounded without one.
Cycle detection already walks the chain; the cap is the walk length. Spanning
task → subtask only, it stops binding in practice.

### 6.2 Data model — migration `0006_task_hierarchy` {#sec-6.2}

```sql
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic'));

-- A task has at most one parent. Two child_of edges out of one task are legal
-- today, and web.go:167 silently keeps whichever was inserted last.
CREATE UNIQUE INDEX task_edges_single_parent
    ON task_edges (from_task) WHERE type = 'child_of';

-- Child lookups (WHERE to_task = $1 AND type = 'child_of') have no usable
-- index: the unique constraint leads with from_task.
CREATE INDEX task_edges_children
    ON task_edges (to_task) WHERE type = 'child_of';
```

The `.down.sql` drops both indexes and restores the four-kind CHECK, failing if
any `kind = 'epic'` row survives. The kind enum has since moved on again — §1.1
states the current CHECK, which carries no `epic`.

### 6.3 Never claimable {#sec-6.3}

One predicate in the `readyCandidates` query (`ranking.go:62-72`) keeps a task
that has children out of the ready set. The pre-029 form of that predicate was a
test on the declared kind:

```sql
AND t.kind <> 'epic'
```

The worktree is the unit of Worklode work (spec 008) and a container has nothing
to check out, so `lode next` must never hand an agent one. Decomposition work
that genuinely needs a worktree becomes a child task.

### 6.4 State machine of a task with children {#sec-6.4}

The state of a task with children is driven entirely by those children.
`legalTransitions` (`tasks.go:66`) is global, so the restriction is enforced in
`Transition` as a guard rather than by a second table.

| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | manual (`lode task ready`) |
| `ready` | `in_progress` | any child started or closed, including abandoned |
| `in_progress` | `merged` | every child closed, at least one delivered |
| `in_progress` | `abandoned` | every child abandoned, or manual `lode task abandon` |
| `merged` | `ready` | existing reopen path, when a child reopens |

`in_review`, `deployed_dev`, `deployed_prod`, and `released` are rejected for a
task with children. Those states are earned by observed deploy facts about a
specific commit (§5) and a container has no commit of its own.
`ResolveDelivery` (`delivery_resolve.go:78`) returns early for a task with
children rather than relying on the commit join never matching; the pre-029 guard
tested `kind = 'epic'`.

Reusing `merged` as the terminal state is what lets a completed parent stop
blocking whatever points at it: a task with children cannot advance past `merged`,
so it is closed there in every repo — the one state-fixed case of the otherwise
per-repo predicate §1.3 defines. `0005_delivery` deliberately removed the old
`done` state and it is not revived. For a task with children, read `merged` as
"all children delivered".

### 6.5 Roll-up {#sec-6.5}

Two distinct mechanisms. Conflating them is the usual failure mode.

**Progress — derived, never stored.** `closed_children / total_children` over
direct children, computed on read for `lode task show`, `lode board`, and the
web page. Two counts, no migration, no resolver, no event-log noise.

**Closure — stored, one transition per event.** `ResolveHierarchy(tx, now,
parentID, eventID)` reads the parent's children and applies the table above.

Edge cases the resolver must get right:

- **Zero children.** No roll-up fires. A task with no children is an ordinary
  task and stays where it is.
- **All children abandoned.** Rolls up to `abandoned`. Treating abandonment as
  delivery would report cancelled work as shipped.
- **Mixed abandoned and delivered.** Rolls up to `merged` — some of the parent's
  work landed.
- **Reopen.** A child returning to `ready` puts the parent back to `ready` via
  the existing reopen transition. Asymmetric roll-up produces boards that lie.

### 6.6 Roll-up hooks into `Transition` {#sec-6.6}

There are eleven `Transition` call sites across `internal/api`,
`internal/hooks`, and `internal/store`. Hooking each one would leave the
invariant one forgotten call site away from breaking.

Instead, `Transition` (`tasks.go:154`) ends with: if the task has a parent, call
`ResolveHierarchy` on it with the same `tx`, `now`, and `eventID`. The child's
own event is the correct attribution for the parent's derived move, so the
timeline explains itself with no synthetic event.

Recursion terminates: a subtask resolves its parent, that parent resolves
nothing (depth cap 2), and cycles are impossible by `AddEdge`'s existing check.

### 6.7 API {#sec-6.7}

- `POST /api/v1/tasks/{id}/edges` gains validation for `child_of`: reject a
  second parent (409, `ErrEdgeExists` shape), a cross-project edge (422), and
  an edge exceeding the depth cap (422).
- `AddEdge` (`tasks.go:401`) returns the walk length from `reachesViaChildOf`
  instead of a bool, and enforces the cap.
- `POST /api/v1/tasks` accepts `parent`, creating the task and its `child_of`
  edge in one transaction — no window where the child exists unparented.
- `GET /api/v1/tasks/{id}` gains a `hierarchy` object: `parent` (id, title,
  state, or null) and `progress` (`{closed, total}`), both derived.
- `POST /api/v1/tasks/{id}/decompose` — see below.

### 6.8 CLI {#sec-6.8}

Symmetric with the existing `block`/`unblock` pair:

```
lode task add --parent <id> …            create a child in one round trip
lode task parent <id> --under <parent-id>  adopt an existing task
lode task unparent <id>
lode task tree [<id>]                    hierarchy with per-parent progress
lode task list --parent <id>
lode task decompose <id> --into "A" "B"  see below
```

`lode task show` gains a `Parent:` line and, for a task with children,
`Progress: 3/7`. `lode board` groups a parent's children under it.

### 6.9 Brief — exactly one hop up {#sec-6.9}

`store.Brief` (`brief.go:18`) gains `Parent *Task`, populated with ID, title,
and state only. An agent should know its task belongs to "Delivery lifecycle"
without spelunking; the full ancestry and the sibling list are both unbounded
and stay out. The field follows the reserved-shape convention already used for
`GoverningDesign`/`AffectedComponents`/`DefinitionOfDone`.

### 6.10 Decomposition {#sec-6.10}

```
lode task decompose <id> --into "Title A" "Title B" "Title C"
```

One transaction: clear `needs_decomposition`, create the N children inheriting
project, priority, and concern from the parent, wire the `child_of` edges, and
leave the children `draft`. The parent's kind is not touched (029 §2). Rejected
when the parent holds an active lease — decomposing work someone is holding is a
coordination bug.

This is what makes the `needs_decomposition` gate actionable: an oversized task
becomes its own tracking task plus the pieces, in place, keeping its id and
every reference to it.

## 7. Postgres schema & data layer {#sec-7}

The backbone is built directly on Postgres — a fresh schema baseline.

**Driver & connection.** `github.com/jackc/pgx/v5`, registered as a `database/sql` driver
via `github.com/jackc/pgx/v5/stdlib` (driver name `"pgx"`), so store functions stay
`*sql.Tx`-typed. `Open` opens a **real pool** (`SetMaxOpenConns` sized to server
concurrency, `SetMaxIdleConns`, `SetConnMaxLifetime`) against a `postgres://…` DSN.
Deterministic clock injection (`nowFn` / `SetNowFunc`) is retained (determinism lens).

**Migrations (golang-migrate).** `database/postgres` driver; source `file://` or embedded
via `iofs` (use the `golang-migrate` skills for authoring/lint/round-trip). The baseline
schema uses `bigint GENERATED ALWAYS AS IDENTITY` keys, `timestamptz` timestamps,
`boolean` flags (`actors.admin`), `jsonb` for `payload`/`change`,
a `leases.worktree` column with a `leases_active_worktree` partial unique index, and
partial unique indexes + `CHECK`s. FKs are enforced by default. `projects.deploy_gated`
is dropped: delivery gating is the per-repo `done_state` plus the fact tables of §5.2.

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

## 8. Claim, lease, and reopen surface {#sec-8}

- `POST /api/v1/tasks/{id}/claim` — request field `session_id` → **`worktree`**
  (`ttl` unchanged); response `Lease.session_id` → `Lease.worktree`.
- `POST /api/v1/tasks/{id}/renew`, `POST /api/v1/tasks/{id}/release` — unchanged shape;
  holder identity is `(actor, worktree)`.
- `lode task claim|renew|release` — a `--worktree` value replaces the
  session flag; hooks (spec 008) supply it. `--json` output carries `worktree`.
- The claim and claim-next responses also carry a server-derived `branch`, rendered
  from `LODE_BRANCH_TEMPLATE` (default `{{ .id }}-{{ .slug }}`); against a server too
  old to send one the CLI falls back to the bare `<id>-<slug>`.
- **New reopen path** — `lode task reopen <id>` →
  `merged|deployed_dev|deployed_prod|released|abandoned → ready`
  (`task.reopened` event).
- `claim --next` endpoint/flags (`--project`, `--strict-focus`) are **defined in spec
  005**; this spec only guarantees the atomic `Claim` primitive it calls.

No MCP (D14, Q14.1) — agents drive `lode --json`; no per-tool schema tokens in context.

## 9. Testing {#sec-9}

**Per-project keys.**

- Per-project counters: two projects each start at 1 and increment independently.
- Key validation: rejects bad format, duplicate key, and missing key.
- Backfill migration: an existing `WL-1…11` project yields key `WL`,
  `next_task_num` 12; a task-less project gets the id-derived fallback.
- Parser generalization: `SW-3` matches branch/dir/body/ranking helpers.
- Update existing tests that assert literal `WL-` where a second project is now
  in play.

**Delivery.**

- Fixture-based handler tests for `push` and `deployment_status`
  (`testdata/github` style).
- Table-driven resolver tests feeding identical fact sets in every arrival
  order, asserting identical outcomes.
- One end-to-end test: claim → branch push → merge → dev deploy (GitHub +
  Flux) → prod deploy → `deployed_prod`.

**Hierarchy.**

- Single parent: a second `child_of` out of one task is rejected; the existing
  `UNIQUE (from_task,to_task,type)` still rejects an exact duplicate.
- Depth: a third level is rejected; the existing cycle test still passes.
- Cross-project: rejected.
- Ready set: a task with children never appears in `claim --next`, including
  when it is `ready`, unblocked, and top-ranked by every other factor.
- Roll-up forward: first child to `in_progress` moves the parent off `ready`;
  last child closed moves it to `merged`.
- Roll-up edge cases: zero children (no move), all-abandoned (`abandoned`),
  mixed (`merged`), child reopen (parent back to `ready`).
- Roll-up attribution: the parent's `state_log` row carries the child's
  `event_id`.
- Parent delivery states: `ResolveDelivery` leaves a task with children alone
  even with commit and deploy facts attributed to it.
- Depth-2 recursion: a subtask closing resolves its task, which resolves that
  task's own parent, in one transaction.
- `decompose`: creates children, clears the flag, and is rejected under an
  active lease.
- Brief: `Parent` populated one hop, absent for a root task.

## 10. Out of scope {#sec-10}

- **Renaming or re-keying an existing project** (immutable by decision),
  renumbering existing IDs, and any UI beyond the `KEY` column in `project list`.
- **Child ordering / rank.** Roll-up and progress do not need it.
- **Blocker inheritance.** `blocks` edges compose already; children do not
  inherit a parent's blockers.
- **Cross-project hierarchies.** Requires a roll-up and board model that spans
  task-id namespaces; revisit if a real multi-repo initiative needs it.
- **Parent-level estimates or burndown.** Progress is a count of children.
- **Graph projection.** The `wl:` vocabulary for hierarchy belongs to spec 006.

## 11. Dependencies {#sec-11}

- **Upstream:** none — this is the foundation spec. External: pgx v5, golang-migrate
  Postgres driver, a running Postgres (CNPG). Sunstone skills:
  `kubernetes` (CNPG), `golang-migrate:*` (author/lint/k8s-job/round-trip).
- **Downstream (depend on 004):** 005 (ranking calls `Claim`; `concern`/`focus` columns
  extend `tasks`), 006 (projects Task/edges/events into the graph via IRI), 008 (hooks
  drive claim/renew/release and supply `worktree`).

## 12. Open questions {#sec-12}

1. ~~Reopen target~~ — **RESOLVED: `done → ready`** (forces a fresh claim; keeps the
   invariant "no `in_progress` without a live lease").
2. **Worktree identity canonical form** — `<host>:<abs-path>` vs git worktree UUID vs
   the deterministic worktree name (then spelled `wt/<id>-<slug>`, since retired —
   §1.2). Cross-host reclaim implications. The backbone treats it opaque; exact form
   is fixed in spec 008.
3. **Isolation level** — confirm READ COMMITTED + `FOR UPDATE` + unique-index backstop
   (vs SERIALIZABLE).
4. **Sweeper under multiple replicas** — rely on event idempotency alone, or gate the
   sweep with a Postgres advisory lock to elect a single sweeper?
5. **Task ↔ GitHub Issue mirror** *(new; from 007 review).* A backbone Task mirrors bidirectionally
   to a GitHub Issue so PRs can join it the native way (`Closes #N`). The backbone owns the Task and
   the mirror link (projected to the graph as `ls:mirrors`, spec 006); the plugin (008) creates and
   syncs the Issue. Open: create-on-task-create vs. lazy; who wins on divergent edits
   (backbone-authoritative?); handling tasks with no mirror yet.
6. **RESOLVED — Q018.1 — Does an epic need wrap-up work?** No. Closure is automatic, and a
   final integration or documentation step is a child task rather than a reason
   to make closure manual. Revisit if real usage contradicts it.
7. **RESOLVED — Q018.2 — Should `lode task done` on an epic be an error or a manual
   override?** An error. `done` is `in_review -> merged` and `in_review` is
   forbidden for epics, so the kind guard in `Transition` rejects it with a
   message naming the roll-up rule. There is no override.

## 13. Acceptance criteria {#sec-13}

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
   with **no ranking logic** in the backbone (ranking is spec 005).
6. State machine enforces §5.1 including reopen; `merged→ready` and
   `abandoned→ready` produce a `task.reopened` event and `state_log` row; every illegal
   transition is `ErrBadTransition`.
7. Every task/lease/edge mutation commits atomically with its `events` row and carries
   provenance via `state_log.event_id`; `RecordEvent` idempotency holds (redelivered id
   does not double-apply); the expiry sweep is idempotent and reverts expired
   in-progress tasks to `ready`.
8. `blocks` gates claimability and `child_of` cycle detection rejects cyclic edges
   (`ErrCycle`) — both verified atomically inside the claim/edge transactions.
