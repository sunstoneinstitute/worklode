# Spec 013 — Reconciliation & setup diagnosis

**Status:** spec · **Umbrella:** `000-umbrella-architecture.md` · **Depends on:** 004 (execution
backbone), the delivery lifecycle already shipped in `internal/store/delivery_resolve.go`.
**Amended by:** 014 (design docs as graph objects — supersedes engine 3 and `task_docs`)

## Purpose & scope

Worklode learns about reality through webhooks. When a webhook never arrives — the GitHub App
isn't installed, the repo isn't mapped to a project yet, the secret is wrong, the server was
down — the backbone silently keeps a stale picture: a task sits in `in_review` whose PR merged a
week ago, or in `merged` whose release shipped. Nothing today surfaces that, and nothing repairs it.

This spec covers, and only covers:

- **`lode reconcile`** — recovering task and spec activity the ingestion path missed.
- **`lode project doctor`** — telling an operator that ingestion is broken for a repo, before
  anyone goes hunting for missing tasks.
- **`lode doctor`** — telling a developer why their own setup misbehaves.
- The **`task_docs`** link and the **`events.applied_at`** marker the above require.

> **Amended by 014 §6.** Only `events.applied_at` remains in scope; the `task_docs` link and the spec-drift engine are superseded. Engines 1 and 2 are untouched.

Out of scope (reference, do not duplicate): architectural drift between declared and observed
graph layers (007 — a different diff over different entities, blocked on 006); promoting *untracked*
GitHub work into new tasks (that is `lode inbox`, already shipped); the KG projection (006).

**Relationship to 007.** Spec 007's `lode drift` compares *declared architecture* against *observed
code*. This spec compares *what the backbone recorded* against *what GitHub actually did*. Both
reconcile intent with reality; they share no entities, no store, and no dependencies. The names
must stay distinct.

---

## Command surface

| Command | Audience | Auth | Job |
|---|---|---|---|
| `lode doctor` | developer | none (works offline) | is *my* setup correct |
| `lode project doctor [repo]` | operator | admin | is ingestion working for *this repo* |
| `lode reconcile [--repo X \| --task Y] [--since D] [--dry-run]` | operator | admin | repair what ingestion missed |

The split is a permission boundary, not a cosmetic one: `doctor` is what every developer runs when
their client misbehaves and must need no privileges; the other two read across the whole org.

### `lode doctor`

Runs entirely client-side and must produce useful output with the server unreachable. Checks, in
order, each reporting pass/fail **and the fix for that failure**:

1. Config file found — which one, and where the `.worklode`/`.lode` walk-up located it.
2. `server` set and reachable.
3. Token present (OS keychain or `LODE_TOKEN`) and accepted — via `GET /api/v1/whoami`.
4. `current_project` set, and the project exists.
5. Git hooks installed in this repo (`lode install`).
6. When run inside a worktree: does it map to a task, and does that task hold a live lease.

Exits non-zero if any check fails, so it is usable from a hook or CI step.

### `lode project doctor [repo]`

Per mapped repo, reported from the server:

- **App installed** — confirmed by minting an installation token (`githubauth.AppAuth`).
- **Last webhook received**, and the event types seen, from the `events` table.
- **Unapplied events** — count of `*.ignored` and nil-apply events still awaiting replay.
- **Unmapped senders** — repos that have sent events but map to no project.

No argument reports every mapped repo. A repo that has never received a webhook, or whose last
delivery predates its mapping, is the signal that sends an operator to `lode reconcile`.

---

## `lode reconcile`

Three engines behind one command, run in order, cheapest first. Facts are repaired; findings are
reported. `--dry-run` suppresses the writes of engines 1 and 2; engine 3 never writes.

`--repo` / `--task` / `--since` bound the candidate set. Unbounded is the scheduled-caller case.
`--since` accepts an RFC 3339 date or a Go duration (`720h`), resolved against the server clock.

**Implementation order.** Three separable phases, shipped in engine order: engine 1 is
self-contained and immediately useful once the apply refactor lands; engine 2 carries the GitHub
pagination and rate-limit work; engine 3 depends on `task_docs` and its backfill. `lode doctor` and
`lode project doctor` can land alongside any phase — `project doctor` is most useful before
engine 1, since it is what tells an operator to run reconcile at all.

### Engine 1 — replay stored events

Events already in the database that were recorded with a nil apply: `*.ignored` (the repo was not
mapped when the delivery arrived, `internal/hooks/github.go:126`) and unhandled actions. The
payload is intact in `events.payload`, so this engine is offline and costs nothing.

**Required refactor.** Apply routing is today a method on `githubHandler`, bound to the HTTP
envelope (`applyFunc`, `internal/hooks/github.go:167`). Replay needs it extracted to a
transport-independent `Apply(tx, st, source, eventID, type, payload)`, called by both the webhook
handler and the replayer. This is the highest-risk change in the spec and is sequenced first.

**Provenance.** A replayed apply is passed the *original* event's id, so any resulting `state_log`
transition points at the real GitHub event. The timeline reads correctly — the event was simply
applied late.

**Completion marker.** `events.applied_at` is set by both the webhook path and the replayer. The
applies are idempotent (upserts, and `Transition` guards on the from-state), so re-running is
harmless; the marker exists so reconcile can find outstanding work without rescanning history.

### Engine 2 — poll GitHub

Candidate tasks: not `done`/`abandoned`, plus tasks that have landed but sit below their repo's
`done_state`. For each, mint an installation token and ask GitHub the current truth about the
entities the task is linked to:

- its PRs' real `state` / `merged` / `merge_commit_sha`;
- whether its recorded commits are on the default branch;
- which releases contain those commits.

Missing facts are written through the existing `UpsertPR` / `InsertTaskCommit` /
`AppendMainCommit`, then `ResolveDelivery` runs. Because `ResolveDelivery` derives delivery state
from recorded facts rather than from event ordering, repairing facts is sufficient — there is no
state machine to replay and no ordering hazard.

**Provenance.** One `source='system'` event per run (type `reconcile.poll`, `external_id` = run
id); facts and transitions attribute to it. This records the truth: the task advanced because
reconcile observed it, not because a webhook arrived.

**Rate limits.** Requests are batched per repo against one installation token. `--since` and
`--repo` are the intended controls for large orgs; an unscoped run over every non-terminal task is
the scheduled case.

### Engine 3 — spec-doc drift

> **Superseded by 014 §6.** The git-mtime heuristic is replaced by the exact, section-scoped stale-claim query over `.worklode/implements.yaml`; engine 3 should be removed rather than built.

Report-only; there is nothing to write. For each `spec`-kind task with a linked doc, compare the
last commit touching that path on the default branch against the task's closure time from
`state_log`:

- **doc changed after closure** — the spec and its implementation have diverged;
- **doc path does not resolve** — a spec task pointing at a file that no longer exists;
- **doc with no spec task** — a design doc under a tracked path that nothing tracks.

Specs are already first-class here: `kind='spec'` is a task kind
(`deploy/base/migrations/0001_baseline.up.sql:53`) and WL-1…WL-7 are exactly that. No knowledge
graph is required. What is missing is the structured link — today a spec task names its file only
in prose (`Source: docs/specs/007-drift-and-overview.md` in the body).

### Output

One report per run, per engine: what was repaired, what was found. `--json` for scheduled callers.

---

## Data model

**`task_docs`** — a table rather than a column, because a spec task can govern several files:

```sql
CREATE TABLE task_docs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    repo       text NOT NULL,
    path       text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (task_id, repo, path)
);
```

Populated by `lode task add --doc <path>` (repeatable). Backfilled by parsing the `Source: …`
lines already present in task bodies.

> **Superseded by 014 §6.** `task_docs` gives way to `.worklode/implements.yaml`; only `events.applied_at` survives from this section.

**`events.applied_at timestamptz`** — nullable; set when an event's apply completes, by either path.

The down migration drops both; the repo's `migrate up`/`down` round-trip check covers it.

---

## API

All under the existing `s.auth(...)` bearer middleware.

| Endpoint | Gate | Returns |
|---|---|---|
| `POST /api/v1/reconcile` | `requireAdmin` | the run report; body `{repo?, task?, since?, dry_run}` |
| `GET /api/v1/repos/doctor[?repo=]` | `requireAdmin` | the ingestion-health report |
| `GET /api/v1/whoami` | auth only | calling actor's id, kind, admin flag |

`POST /api/v1/reconcile` is **synchronous**. A scoped run is fast, and the unscoped org-wide run is
the scheduled case where waiting is acceptable. If runs begin timing out in practice, that is the
signal to make it a job — not something to build up front.

`GET /api/v1/whoami` is a genuine gap independent of this spec: the CLI has no way today to ask who
a token belongs to.

---

## Testing

- **Replay:** seed `*.ignored` events (the existing tests at `internal/hooks/github_test.go:566`
  and `push_test.go:230` already produce those rows), map the repo, replay, assert the typed tables
  and `state_log` match what a live delivery would have produced — and that a second replay is a
  no-op.
- **Poll:** an `httptest.Server` standing in for the GitHub API via `AppAuth.BaseURL` (already
  documented as test-overridable, `internal/githubauth/app.go:32`). Seed a task whose PR the
  backbone believes is open and GitHub reports merged; assert the facts are written, the task
  advances, and the transition attributes to the `reconcile.poll` system event. Run twice; assert
  convergence.
- **Spec drift:** seeded `task_docs` and a fake commit history — a doc modified after closure
  reports, one modified before does not.

> **Obsolete with engine 3 (014 §6).** There is no `task_docs` table and no mtime comparison to test.

- **Both doctors:** table-driven over broken-setup fixtures, asserting exit code and that each
  failure names its fix.
- `store` tests run against ephemeral Postgres, as the rest of the suite does.

---

## Dependencies

- **004 — execution backbone:** tasks, events, `state_log`, `Transition`.
- **`internal/hooks/`:** the apply routing engine 1 extracts and reuses.
- **`internal/githubauth`:** installation tokens for engine 2. Already built.
- **`internal/store/delivery_resolve.go`:** `ResolveDelivery`, unchanged.

No dependency on 006, 007, or 009.

## Open questions

1. **Candidate set for engine 2.** "Not terminal, or landed but below `done_state`" may still be
   too large for an org-wide unscoped run. Confirm against real task counts before assuming
   `--since` is optional.
2. **Tracked doc paths for engine 3's third finding** ("doc with no spec task"). Per-project
   configuration, or a convention like `docs/specs/**` and `docs/adr/**`?

> **Closed by 014 §10.** Per-project configuration, not convention — and temporary, since documents move into the graph.

3. **Scheduled invocation.** Out of scope here (the command is on-demand), but if reconcile proves
   it should run continuously, does it become a server loop — and does that need the sweeper's
   single-instance election (004, open Q4)?

## Acceptance criteria

- **Replay:** an event recorded as `*.ignored` before its repo was mapped, then replayed, produces
  exactly the typed-table and `state_log` result a live delivery would have; the resulting
  transition references the original event id; `applied_at` is set; a second replay changes nothing.
- **Poll:** a task whose PR merged while ingestion was down reaches its correct delivery state
  after one reconcile run, with the transition attributed to a `reconcile.poll` system event; a
  second run is a no-op. `--dry-run` reports the same repair and writes nothing.
- **Spec drift:** a spec task whose doc changed after closure is reported; one whose doc changed
  before is not; a spec task with an unresolvable doc path is reported.

> **Obsolete with engine 3 (014 §6).** Replaced by 014's stale-claim and orphaned-claim criteria (its acceptance criterion 8).

- **`lode project doctor`** identifies a mapped repo with no App installation, a repo whose last
  webhook predates its mapping, and a repo sending events that maps to no project.
- **`lode doctor`** exits non-zero and names the fix for each of: missing config, unreachable
  server, invalid token, unset `current_project`, missing git hooks.
- Every command emits deterministic `--json`.
