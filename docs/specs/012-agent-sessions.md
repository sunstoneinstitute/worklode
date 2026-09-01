---
status: draft
issued: 2026-07-25
amendedBy:
  "#sec-1":
  - 052-project-overhead-cost.md#sec-1
  "#sec-3":
  - 052-project-overhead-cost.md#sec-2
  "#sec-4":
  - 052-project-overhead-cost.md#sec-3
---
# Spec 012 — Agent sessions

## 0. Problem {#sec-0}

Worklode knows which actor holds a lease on a task, but not which coding-agent
session is actually working in it. Claude Code's `session_id` already reaches
`internal/hookrun` on every `session-start` payload; the only place it lands is
a local marker file (`worklode-session.json`, in the worktree-private git dir),
which answers "is a process alive on this box" and nothing more. The backbone
cannot answer:

- Which agent sessions are running right now, and on which tasks?
- Which sessions worked a task, and for how long?
- What did a task cost in tokens? (Not solved here, but nothing can solve it
  until sessions are recorded.)

Worklode is not Claude-Code-specific, so the model must fit other coding
agents.

## 1. Schema {#sec-1}

> **Amended by spec 052 §1.** Usage with no task to bill to — a main-checkout
> orchestration session, or a worktree this actor no longer holds the lease
> on — is recorded in two new tables, `project_overhead_usage` and
> `project_daily_overhead_cost`, mirroring `agent_session_usage` and
> `project_daily_cost` but keyed to a project directly rather than through a
> lease. Nothing below changes.

One new table; `leases` is unchanged.

```sql
CREATE TABLE agent_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lease_id            bigint NOT NULL REFERENCES leases(id) ON DELETE RESTRICT,
    agent               text NOT NULL CHECK (agent IN
                          ('claude-code','codex','copilot','cursor','aider','opencode','pi','amp','other')),
    agent_version       text,
    external_session_id text NOT NULL,
    started_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    ended_at            timestamptz,
    input_tokens        bigint,
    output_tokens       bigint,
    cost_amount         numeric(12,6),
    cost_currency       text NOT NULL DEFAULT 'USD'
                          CONSTRAINT agent_sessions_cost_currency_format
                          CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT agent_sessions_lease_session_unique
      UNIQUE (lease_id, agent, external_session_id)
);
```

No separate index on `lease_id`: the unique constraint's index already leads
with that column, so it serves every by-lease lookup, including the foreign key
check. Constraints are named rather than left to Postgres, so store code and
tests can refer to them.

Why a child table rather than columns on `leases`: a lease routinely outlives a
single agent session. `handleSessionStart` renews or re-acquires an existing
lease, so one lease covers every restart, `/clear`, and next-day resumption.
Columns on `leases` would record only the newest session and leave per-session
cost with nowhere to live.

`agent`'s CHECK constraint mirrors the existing style of `events.source` and
`actors.kind`, and allows the harnesses spec 008 §19 names; adding a tool is a
one-line migration. `external_session_id` is
the tool's own identifier (a UUID for Claude Code), namespaced by `agent`
because nothing guarantees two tools won't collide.

The unique key is **per lease**, not global. A lease can expire mid-session and
be re-claimed by the same live session — `lode worktree resume` does exactly this — and
that must produce a second row, not a constraint violation.

Concurrent open sessions on one lease are permitted (no partial unique index).
Leases are already worktree-bound, so this only happens when someone runs two
agents in one directory; it is harmless and not worth a constraint.

Token and cost columns ship nullable and unpopulated. Nothing computes them in
this cut; they are in the migration so the table is shaped right.

**Superseded by migration `0008_session_cost`.** The two token columns turned
out to have the wrong shape to compute cost from: a vendor prices a prompt in
four separate classes (uncached input, a cache write at each of two TTLs
(time-to-live periods), and a cache read) at rates spanning 0.1x to 2x base
input, and one session mixes models at several-fold different rates. These
columns survive as the session's
headline rollup — `input_tokens` is every prompt-side class summed — and the
billable detail moved to `agent_session_usage`, keyed by (session, day, model,
speed) and priced from the effective-dated `model_prices` table. See the
comments at the head of that migration for the model.

`agent_version` is likewise reserved: it is plumbed through every layer, but
the Claude Code hook payload carries no version, so it is always empty today.
It is there for agents that do report one.

Cost is an amount plus an ISO 4217 currency code, never a bare USD number: not
every vendor bills in dollars (Mistral bills EUR). The currency defaults to
`USD` and is NOT NULL, so an amount can never sit there without a currency to
give it meaning; `cost_amount IS NULL` is what says "no cost recorded".

The column default alone is not enough, because cost is written by
`EndAgentSession`'s UPDATE and a column DEFAULT only fires on INSERT.
`EndAgentSession` therefore applies the same default explicitly: an amount
supplied without a currency is stored as USD.

Converting between currencies is a reporting concern and needs a rate source
with a date — out of scope here; the table records what the vendor charged.

## 2. Lifecycle {#sec-2}

- A row is created on the first heartbeat from a session and belongs to
  whichever lease was active at that moment.
- A heartbeat on a row that is already closed re-opens it: `ended_at` goes back
  to NULL. A session that leaves a worktree and returns to it later is working
  that lease again, and must not stay closed. The row then spans first touch to
  last, without recording the gap — per-visit granularity is not something the
  design promises.
- `last_seen_at` is bumped on every heartbeat. Running sessions are
  `ended_at IS NULL AND last_seen_at > now() - interval '30 minutes'`. The
  staleness window is a read-side constant only; nothing writes or enforces it,
  so it can change without a migration.
- `ended_at` is stamped by the session-end hook, **and** by `closeLease` /
  `CloseActiveLease` for any session still open on the lease being closed. A
  swept, released, or completed task therefore never leaves a session that
  looks live, and "what is running now" stays a single-table query. A close by
  this route stamps `ended_at` without emitting an `agent_session.ended` event:
  the lease's own `lease.released` / `lease.expired` event already records the
  transition, so `started`/`ended` pairs in the event stream are deliberately
  unbalanced.

## 3. Store and API {#sec-3}

> **Amended by spec 052 §2.** A third store entry point,
> `ReportProjectSessionUsage`, and a new `POST /api/v1/projects/{id}/session-usage`
> route join `TouchAgentSession`/`EndAgentSession` below — it has no lease to
> check a holder against, because it owns every usage row one session writes in
> a project, including rows whose lease is already gone. There is deliberately
> no overhead-only entry point beside it: a second write path to the same
> `(project, agent, external session id)` key is what let a session's tokens be
> counted twice.
> `ProjectCost` also changes: its report becomes the combined total of
> task-attributed and overhead spend, with overhead's own share broken out
> alongside it. `TaskCost`, below, is unchanged.

Two store functions, shaped like the existing `Claim` / `Renew` / `Release`
family. Both resolve the active lease with `activeLeaseTx` and require the
caller to be its holder, returning `ErrNotFound` for a non-holder — the same
probe-resistant policy as `Renew`, so the two failure cases stay
indistinguishable.

**`TouchAgentSession(ctx, taskID, actorID, agent, version, sessionID, usage)`** —
start-or-heartbeat. Wrapped in `RecordEvent("cli", "agent-session-<leaseID>-<agent>-<sessionID>", "agent_session.started", …)`
whose apply does `INSERT … ON CONFLICT DO NOTHING`. The deterministic external
id makes first-seen idempotent through the events table's `(source,
external_id)` uniqueness. The `last_seen_at` bump is a plain `UPDATE` issued
*outside* the event, because a repeat call takes `RecordEvent`'s already-recorded
path and skips apply entirely — and because a heartbeat is not an event worth
keeping.

That bump also clears `ended_at` (see Lifecycle), and runs in its own
transaction that first takes a locking read on the lease
(`SELECT … FOR SHARE`), skipping the write when the lease has been released.
The holder check that precedes it runs on a plain connection and cannot see a
release landing mid-call; without the lock a heartbeat could leave a
permanently open session on a released lease, which nothing would ever close.

A plain `EXISTS (SELECT … FROM leases …)` in the `UPDATE` does **not** achieve
this. It is uncorrelated, so it is evaluated once against the statement
snapshot, and READ COMMITTED's row re-check covers only the target row's own
predicates — the update still lands after the closing transaction commits.

The session **insert** needs the same lock, for the same reason and with a
worse failure mode: the foreign key does not serialize it, because an insert
takes `FOR KEY SHARE` on the lease row while `UPDATE leases SET released_at`
takes only `FOR NO KEY UPDATE` (`released_at` appears solely in partial
indexes, which are not key attributes). Without the lock a session can be
created *after* the close has already swept the lease's open sessions, and
nothing will ever close it — not `closeLease`, whose lease is gone; not
`EndAgentSession`, which requires an active held lease; not the sweeper, which
walks only unreleased leases.

All four paths that touch both tables — session insert, heartbeat,
`closeLease`, `CloseActiveLease` — lock `leases` before `agent_sessions`, so
they cannot deadlock against each other.

The optional `usage` is the same breakdown `EndAgentSession` takes, written the
same replace-not-accumulate way, and it exists because usage reported only at
a clean end is usage lost whenever there isn't one — a crashed agent, or a
lease the sweeper expires, closes the session with `ended_at` and no spend. It
is written after the touch rather than inside its event apply: a heartbeat
takes `RecordEvent`'s already-recorded path, so usage riding that apply would
land once per session and never again.

**`EndAgentSession(ctx, taskID, actorID, agent, sessionID, usage)`** — records
an `agent_session.ended` event, sets `ended_at`, and writes any token/cost
values supplied by the caller. Its event id is random, not derived from the
session: a session can legitimately be closed more than once on one lease
(exit, re-enter, exit again). Idempotency comes instead from the `ended_at IS
NULL` predicate — a repeat close matches no rows, which fails apply and rolls
the event back, so there is exactly one `agent_session.ended` event per real
close.

HTTP surface, in the existing lifecycle group:

- `POST /api/v1/tasks/{id}/agent-session` — body `{agent, agent_version, session_id, usage?}`
- `POST /api/v1/tasks/{id}/agent-session/end` — body `{agent, session_id, input_tokens?, output_tokens?, cost_amount?, cost_currency?, usage?}`

`cost_amount` crosses the wire as a decimal **string**, so `numeric(12,6)`
round-trips exactly. It is validated in Go against the column's shape, like
`cost_currency` — letting Postgres do the parsing turns a client typo into a
500 and a spurious error-log entry.

Everything is named `agent-session`, never `session`: `internal/api/session.go`
already owns "session" for web and CLI auth.

## 4. Hook wiring {#sec-4}

> **Amended by spec 052 §3.** The table below still names the right calls,
> but `heartbeat`, `session-end`, and `worktree-enter`'s guard no longer
> requires the hook's own directory to carry a task id — only that it is
> inside a git worktree at all — so a main-checkout session reports too, with
> every token that resolves to no task (or to a task this actor no longer
> holds the lease on) billed as project overhead instead of dropped. The
> other rows and rules below are unchanged; see 052 §3 for exactly which
> handlers change and which deliberately do not.

`hookrun.Payload.SessionID` already carries the value. `lode hook` gains three
events (`heartbeat`, `worktree-enter`, `worktree-exit`) and `session-end` gains
a backbone call it does not make today:

| `lode hook` event | Call | Session id from | Claude Code binding |
|---|---|---|---|
| `session-start` | `TouchAgentSession` after `ensureLease` | payload | `SessionStart` |
| `heartbeat` | `TouchAgentSession`, reporting the transcript's usage | payload | `Stop`, `StopFailure`, `SubagentStop`, `Notification` |
| `worktree-enter` | `TouchAgentSession` on the entered worktree's lease | payload | `PostToolUse` matcher `EnterWorktree` |
| `worktree-exit` | `EndAgentSession` for the exited lease's row | payload | *(none — see below)* |
| `pre-commit` | `TouchAgentSession` alongside the existing `RenewLease` | marker file | git `pre-commit` |
| `session-end` | `EndAgentSession` before removing the marker | payload | `SessionEnd` |

Events are named for their role, not for any one tool's event name, so other
agents bind whatever they have.

**Liveness (`heartbeat`).** `Stop` is the backbone of it: one firing per
assistant turn. The other three plug holes `Stop` alone leaves — a session that
would otherwise look dead while it is very much alive:

- `StopFailure` fires *instead of* `Stop` when a turn dies on an API error
  (rate limit, overload, billing). A rate-limited session is stalled, not gone.
- `SubagentStop` covers a single turn that fans out to many subagents and runs
  past the staleness window before any `Stop`.
- `Notification` (`permission_prompt`, `idle_prompt`, `agent_needs_input`)
  covers a session blocked on a human. `Stop` already fired; without this, an
  agent waiting an hour on a permission dialog reads as stale.

**One session, several tasks (`worktree-enter` / `worktree-exit`).** A session
can move between worktrees via `EnterWorktree` / `ExitWorktree`, which means one
session works several tasks in sequence. The schema already models this: entering
opens a row under the *new* lease with the same `external_session_id`, exiting
stamps `ended_at` on the row it leaves. A row therefore reads as "this session
worked this lease, from here to here" — which is also what makes per-task cost
attribution possible later for a session that spanned tasks.

Entering and exiting a worktree also moves the session marker, symmetrically
with session start and end: `worktree-enter` writes it, `worktree-exit` removes
it. The marker is "which session is live in this worktree", and `heartbeatDue`
reads it — without one, heartbeats in an entered worktree would be debounced
off permanently.

**`worktree-exit` has no Claude Code binding.** `ExitWorktree`'s tool input is
`{action, discard_changes}` — no path — and by the time `PostToolUse` fires the
session's cwd has already been restored to the directory being returned *to*.
Falling back to cwd would therefore close the session on the wrong worktree:
a session that entered B from A would, on exiting B, end A's row while still
working A. So `worktree-exit` requires an explicit path in `tool_input` and is
a NOP without one. `worktree-enter` keeps the cwd fallback, where it is correct
— `EnterWorktree` switches cwd to the entered worktree before the hook fires,
and supplies no path at all when creating a worktree by name.

A session that leaves a worktree therefore leaves its row open. That is
acceptable: `last_seen_at` stops advancing, so the row drops out of the
30-minute running window, and the row is closed for good when the lease is
released, expires, or the task completes. The event exists for agents that can
report an exit path, and for explicit invocation.

**Volume control.** These bindings fire more often than `Stop` alone, so the
heartbeat is debounced client-side: `worklode-session.json` gains a
`last_heartbeat_at` field, and a heartbeat within 60s of the recorded one is
skipped without a backbone call. `worktree-enter` / `worktree-exit` are not
debounced — they carry a lease change, not just liveness.

**Not bound, and why:**

- `PostToolUse` *unmatched*, and `PostToolBatch` — hundreds of firings per
  session against a 10s default timeout, for no signal `Stop` lacks. Matched
  `PostToolUse` is a different matter and is used above: the matcher is a tool
  name, so binding `EnterWorktree` costs nothing per ordinary tool call.
- `UserPromptSubmit` — a strict subset of `Stop`, missing autonomous turns.
- `WorktreeCreate` / `WorktreeRemove` — delegation hooks, not notifications:
  binding one makes it *the* worktree creator, replacing Claude Code's built-in
  `git worktree add`, and `EnterWorktree` fails unless the hook prints the path
  it created. Worklode observes rather than creates. Nothing is lost: Claude
  Code's worktrees live under `.claude/worktrees/`, which `worktree.ParseDir`
  rejects, and Worklode's own worktrees (then under `wt/<task-id>`, now under the
`worktree_dir` layout spec 008 defines) are covered by
  `session-start` (auto-resume) and the matched `EnterWorktree` binding above.
- `PreCompact` / `PostCompact` — `SessionStart` already re-fires with
  `source: compact`.
- `TaskCreated` / `TaskCompleted` — these are Claude Code's *in-session todo*
  items, not Worklode tasks. The name collision is a trap: wiring them invites
  a mapping between ephemeral per-turn todos and Worklode's durable task graph,
  and no such mapping exists.

**Two firing rules worth stating,** both already absorbed by the schema:

- `SessionStart` re-fires mid-lifetime (matchers `startup|resume|clear|compact|fork`).
  `TouchAgentSession` is idempotent, so re-entry is a no-op — and `/clear`
  yields a *new* session id under the same lease, which is why the unique key
  is `(lease_id, agent, external_session_id)` and not one row per lease.
- `SessionEnd` fires on `clear` and `resume`, not only on process exit. That is
  the correct reading: one session ends, another begins.

A git `pre-commit` hook has no stdin, so it reads the session id out of the
marker file. With `heartbeat` wired this is opportunistic coverage for agents
that integrate only through git, not the primary liveness mechanism. A marker
that is missing or stale means no heartbeat, not an error.

The marker file otherwise keeps its existing job: it reports process liveness
on one machine, which the backbone cannot.

Agent identity comes from the `LODE_AGENT` environment variable, defaulting to
`claude-code`. Not a CLI flag: `lode hook` sets `DisableFlagParsing` so the
`--next` argv passes through verbatim, and a real flag would fight that.

Both calls inherit the package's two standing rules — every backbone call under
the 2s `backboneTimeout`, and no hook ever fails its triggering event.

## 5. Error handling {#sec-5}

- An unreachable or erroring backbone degrades to a stderr warning; the coding
  session proceeds unaffected.
- A heartbeat for a task the caller no longer holds returns `ErrNotFound` and
  is warned, not retried. Losing a lease means the session is no longer
  authoritative.
- Repeat heartbeats are idempotent by construction (deterministic event
  external id + `ON CONFLICT DO NOTHING`).

## 6. Testing {#sec-6}

- Store tests: start/heartbeat idempotency, non-holder rejection, re-claim of
  the same session id under a new lease, `ended_at` stamped by lease close and
  by the sweeper (`ExpireLeases`).
- API tests in the `internal/api/lifecycle_test.go` style: happy path, 404 for
  non-holder, malformed agent value.
- `hookrun` tests: session-start reports the session, heartbeat bumps
  `last_seen_at`, a heartbeat inside the 60s debounce makes no backbone call,
  worktree-enter/exit open and close a row against the right lease, session-end
  closes the session, and a failing backbone still exits 0 on every one of them.
- Cost columns: an amount ended without a currency lands as USD (the UPDATE
  path, where the column DEFAULT does not apply); a non-ISO currency code is
  rejected.

## 7. Out of scope {#sec-7}

- ~~Computing tokens and cost.~~ **Done** — see migration `0008_session_cost`,
  `internal/transcript`, and `lode project show`. It landed where this spec
  predicted: `session-end` and `worktree-exit` parse the `transcript_path` the
  payload already carries, and report per-model, per-day token classes that the
  server prices. `heartbeat` reports the same running total, because a session
  that dies or is swept never reaches a clean end at all. The transcript needs deduplicating on message id — one
  assistant message is written once per content block, each line repeating the
  whole usage block — and filtering by the working directory each turn ran in,
  so a session that moves between worktrees does not bill the same tokens to
  two leases.
- A `lode sessions` listing command and any web UI surface.
- Fixing the session marker's pid. `writeSessionMarker` records
  `os.Getpid()` — the pid of the short-lived `lode hook` process, which is dead
  the moment the hook exits — so `sessionMarkerFresh` is effectively always
  false in production, and `offerScan` reads any expired-lease worktree as
  abandoned even when a session is live in it. This predates agent sessions and
  is untouched here; the backbone's own `last_seen_at` is now the better
  liveness signal anyway.
