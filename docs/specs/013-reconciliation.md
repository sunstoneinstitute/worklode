---
status: draft
issued: 2026-07-25
requires:
- docs/specs/004-execution-backbone.md
amendedBy:
  "#sec-1":
    - WL-SPEC-61#sec-2
---
# Spec 013 — Reconciliation & setup diagnosis

## 0. Purpose & scope {#sec-0}

Worklode learns about reality through webhooks. When a webhook never arrives — the GitHub App
isn't installed, the repo isn't mapped to a project yet, the secret is wrong, the server was
down — the backbone silently keeps a stale picture: a task sits in `in_review` whose PR merged a
week ago, or in `merged` whose release shipped. Nothing today surfaces that, and nothing repairs it.

This spec covers, and only covers:

- **`lode reconcile`** — recovering task and spec activity the ingestion path missed.
- **`lode project doctor`** — telling an operator that ingestion is broken for a repo, before
  anyone goes hunting for missing tasks.
- **`lode doctor`** — telling a developer why their own setup misbehaves.
- The **`events.applied_at`** marker the above require.

Out of scope (reference, do not duplicate): architectural drift between declared and observed
graph layers (007 — a different diff over different entities, blocked on 006); promoting *untracked*
GitHub work into new tasks (that is `lode inbox`, already shipped); the KG projection (006).

**Relationship to 007.** Spec 007's `lode drift` compares *declared architecture* against *observed
code*. This spec compares *what the backbone recorded* against *what GitHub actually did*. Both
reconcile intent with reality; they share no entities, no store, and no dependencies. The names
must stay distinct.

---

## 1. Command surface {#sec-1}

> **Amended by spec 061 §2.** `lode reconcile` → `lode task reconcile` and
> `lode project doctor` → `lode project health`; `lode doctor` keeps its name.
> The permission boundary this section draws is unchanged, and the rename
> applies wherever this spec spells the old names, §2's heading included.

| Command | Audience | Auth | Job |
|---|---|---|---|
| `lode doctor` | developer | none (works offline) | is *my* setup correct |
| `lode project doctor [repo]` | operator | admin | is ingestion working for *this repo* |
| `lode reconcile [--repo X \| --task Y] [--since D] [--dry-run]` | operator | admin | repair what ingestion missed |

The split is a permission boundary, not a cosmetic one: `doctor` is what every developer runs when
their client misbehaves and must need no privileges; the other two read across the whole org.

### 1.1 `lode doctor` {#sec-1.1}

Runs entirely client-side and must produce useful output with the server unreachable. Checks, in
order, each reporting pass/fail **and the fix for that failure**:

1. Config file found — which one, and where the `.worklode`/`.lode` walk-up located it.
2. `server` set and reachable.
3. Token present (OS keychain or `LODE_TOKEN`) and accepted — via `GET /api/v1/whoami`.
4. `current_project` set, and the project exists.
5. Git hooks installed in this repo (`lode install`).
6. When run inside a worktree: does it map to a task, and does that task hold a live lease.

Exits non-zero if any check fails, so it is usable from a hook or CI step.

### 1.2 `lode project doctor [repo]` {#sec-1.2}

Per mapped repo, reported from the server:

- **App installed** — read from `GET /repos/{owner}/{name}/installation` (`githubauth.AppAuth`),
  one round trip per repo. Reported as installed / not installed / unchecked: GitHub's own 404 is
  the only "not installed", and a failed or un-run check says so rather than reading as absence.
  The checks run concurrently under one deadline covering the whole check phase, so an org's repo
  count cannot multiply into the response time.
- **Last webhook received**, and the event types seen, from the `events` table.
- **Unapplied events** — count of `*.ignored` and nil-apply events still awaiting replay.
- **Unmapped senders** — repos that have sent events but map to no project.

No argument reports every mapped repo. A repo that has never received a webhook, or whose last
delivery predates its mapping, is the signal that sends an operator to `lode reconcile`.

---

## 2. `lode reconcile` {#sec-2}

> **Renamed.** 061 §2.3 spells this command `lode task reconcile` (see §1's
> amendment note). The heading keeps the name it was published under.

Two engines behind one command, run in order, cheapest first. Facts are repaired; findings are
reported. `--dry-run` suppresses the writes of both.

`--repo` / `--task` / `--since` bound the candidate set. Unbounded is the scheduled-caller case.
`--since` accepts an RFC 3339 date or a Go duration (`720h`), resolved against the server clock.

**Implementation order.** Two separable phases, shipped in engine order: engine 1 is
self-contained and immediately useful once the apply refactor lands; engine 2 carries the GitHub
pagination and rate-limit work. `lode doctor` and `lode project doctor` can land alongside either
phase — `project doctor` is most useful before engine 1, since it is what tells an operator to run
reconcile at all.

### 2.1 Engine 1 — replay stored events {#sec-2.1}

Events already in the database whose apply never completed: `*.ignored` (the repo was not
mapped when the delivery arrived, `internal/hooks/github.go:126`) and deliveries whose apply
failed. The payload is intact in `events.payload`, so this engine is offline and costs nothing.

**Record first, apply second (WL-247).** The webhook path commits the event row in its own
transaction and runs the apply in a second one (`store.RecordEventThenApply`), so a failed apply
answers 500 but leaves the row with `applied_at` NULL — exactly the state this engine repairs —
instead of rolling the delivery back into nonexistence, which no one redelivers. The split is
safe because the applies are already order-safe under replay (below). Dedup follows the marker,
not the row: a redelivered event whose row exists unapplied gets its apply re-run, so GitHub's
manual redelivery is a second remedy alongside replay; only an event already marked applied is a
no-op duplicate.

**Required refactor.** Apply routing is today a method on `githubHandler`, bound to the HTTP
envelope (`applyFunc`, `internal/hooks/github.go:167`). Replay needs it extracted to a
transport-independent `Apply(tx, st, source, eventID, type, payload)`, called by both the webhook
handler and the replayer. This is the highest-risk change in the spec and is sequenced first.

**Provenance.** A replayed apply is passed the *original* event's id, so any resulting `state_log`
transition points at the real GitHub event. The timeline reads correctly — the event was simply
applied late.

**Completion marker.** `events.applied_at timestamptz` — nullable; set when an event's apply
completes, by either the webhook path or the replayer. Re-running is harmless because the applies
are order-safe, not merely idempotent: a replayed event may be older than facts that already
landed, so every fact upsert is guarded on the fact's own last-modified time and cannot overwrite a
newer row (see `store.UpsertPR`), and `Transition` guards on the from-state. The marker exists so
reconcile can find outstanding work without rescanning history. The down migration drops the
column; the repo's `migrate up`/`down` round-trip check covers it.

**Batch size.** One run reads a bounded batch of candidates (oldest first), not the whole backlog:
each candidate carries its entire delivery payload, and the unscoped org-wide run is exactly the
caller whose backlog can be arbitrarily large. A run that fills its batch reports `truncated`, and
the reported error list is capped the same way (the surplus is counted, not listed) because that
list is part of the API response body. Both are re-run signals — an applied event leaves the
candidate set, so running again continues after the last batch rather than repeating it.

### 2.2 Engine 2 — poll GitHub {#sec-2.2}

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

### 2.3 Output {#sec-2.3}

One report per run, per engine: what was repaired, what was found. `--json` for scheduled callers.

---

## 3. API {#sec-3}

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

## 4. Dependencies {#sec-4}

- **004 — execution backbone:** tasks, events, `state_log`, `Transition`.
- **`internal/hooks/`:** the apply routing engine 1 extracts and reuses.
- **`internal/githubauth`:** installation tokens for engine 2. Already built.
- **`internal/store/delivery_resolve.go`:** `ResolveDelivery`, unchanged.

No dependency on 006, 007, or 009.

## 5. Open questions {#sec-5}

1. **Candidate set for engine 2.** "Not terminal, or landed but below `done_state`" may still be
   too large for an org-wide unscoped run. Confirm against real task counts before assuming
   `--since` is optional.
2. **Tracked doc paths.** The paths a project's design documents live under were per-project
   configuration, not a convention like `docs/specs/**` and `docs/adr/**` — and temporary, since
   documents move into the graph. The finding that raised the question has since been retired.
3. **Scheduled invocation.** Out of scope here (the command is on-demand), but if reconcile proves
   it should run continuously, does it become a server loop — and does that need the sweeper's
   single-instance election (004, open Q4)?

## 6. Testing {#sec-6}

- **Replay:** seed `*.ignored` events (the existing tests at `internal/hooks/github_test.go:566`
  and `push_test.go:230` already produce those rows), map the repo, replay, assert the typed tables
  and `state_log` match what a live delivery would have produced — and that a second replay is a
  no-op.
- **Poll:** an `httptest.Server` standing in for the GitHub API via `AppAuth.BaseURL` (already
  documented as test-overridable, `internal/githubauth/app.go:32`). Seed a task whose PR the
  backbone believes is open and GitHub reports merged; assert the facts are written, the task
  advances, and the transition attributes to the `reconcile.poll` system event. Run twice; assert
  convergence.
- **Both doctors:** table-driven over broken-setup fixtures, asserting exit code and that each
  failure names its fix.
- `store` tests run against ephemeral Postgres, as the rest of the suite does.

## 7. Acceptance criteria {#sec-7}

- **Replay:** an event recorded as `*.ignored` before its repo was mapped, then replayed, produces
  exactly the typed-table and `state_log` result a live delivery would have; the resulting
  transition references the original event id; `applied_at` is set; a second replay changes nothing.
- **Poll:** a task whose PR merged while ingestion was down reaches its correct delivery state
  after one reconcile run, with the transition attributed to a `reconcile.poll` system event; a
  second run is a no-op. `--dry-run` reports the same repair and writes nothing.
- **`lode project doctor`** identifies a mapped repo with no App installation, a repo whose last
  webhook predates its mapping, and a repo sending events that maps to no project.
- **`lode doctor`** exits non-zero and names the fix for each of: missing config, unreachable
  server, invalid token, unset `current_project`, missing git hooks.
- Every command emits deterministic `--json`.
