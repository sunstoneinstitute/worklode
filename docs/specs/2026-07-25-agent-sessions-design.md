# Agent sessions — design

**Date**: 2026-07-25
**Status**: Approved design, pending implementation plan

## Problem

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

## Schema

One new table; `leases` is unchanged.

```sql
CREATE TABLE agent_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lease_id            bigint NOT NULL REFERENCES leases(id) ON DELETE RESTRICT,
    agent               text NOT NULL CHECK (agent IN
                          ('claude-code','codex','cursor','aider',
                           'opencode','pi','amp','other')),
    agent_version       text,
    external_session_id text NOT NULL,
    started_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    ended_at            timestamptz,
    input_tokens        bigint,
    output_tokens       bigint,
    cost_amount         numeric(12,6),
    cost_currency       text NOT NULL DEFAULT 'USD'
                          CHECK (cost_currency ~ '^[A-Z]{3}$'),
    UNIQUE (lease_id, agent, external_session_id)
);
CREATE INDEX agent_sessions_lease ON agent_sessions (lease_id);
```

Why a child table rather than columns on `leases`: a lease routinely outlives a
single agent session. `handleSessionStart` renews or re-acquires an existing
lease, so one lease covers every restart, `/clear`, and next-day resumption.
Columns on `leases` would record only the newest session and leave per-session
cost with nowhere to live.

`agent`'s CHECK constraint mirrors the existing style of `events.source` and
`actors.kind`; adding a tool is a one-line migration. `external_session_id` is
the tool's own identifier (a UUID for Claude Code), namespaced by `agent`
because nothing guarantees two tools won't collide.

The unique key is **per lease**, not global. A lease can expire mid-session and
be re-claimed by the same live session — `lode resume` does exactly this — and
that must produce a second row, not a constraint violation.

Concurrent open sessions on one lease are permitted (no partial unique index).
Leases are already worktree-bound, so this only happens when someone runs two
agents in one directory; it is harmless and not worth a constraint.

Token and cost columns ship nullable and unpopulated. Nothing computes them in
this cut; they are in the migration so the table is shaped right.

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

## Lifecycle

- A row is created on the first heartbeat from a session and belongs to
  whichever lease was active at that moment.
- `last_seen_at` is bumped on every heartbeat. Running sessions are
  `ended_at IS NULL AND last_seen_at > now() - interval '30 minutes'`. The
  staleness window is a read-side constant only; nothing writes or enforces it,
  so it can change without a migration.
- `ended_at` is stamped by the session-end hook, **and** by `closeLease` /
  `CloseActiveLease` for any session still open on the lease being closed. A
  swept, released, or completed task therefore never leaves a session that
  looks live, and "what is running now" stays a single-table query.

## Store and API

Two store functions, shaped like the existing `Claim` / `Renew` / `Release`
family. Both resolve the active lease with `activeLeaseTx` and require the
caller to be its holder, returning `ErrNotFound` for a non-holder — the same
probe-resistant policy as `Renew`, so the two failure cases stay
indistinguishable.

**`TouchAgentSession(ctx, taskID, actorID, agent, version, sessionID)`** —
start-or-heartbeat. Wrapped in `RecordEvent("cli", "agent-session-<leaseID>-<agent>-<sessionID>", "agent_session.started", …)`
whose apply does `INSERT … ON CONFLICT DO NOTHING`. The deterministic external
id makes first-seen idempotent through the events table's `(source,
external_id)` uniqueness. The `last_seen_at` bump is a plain `UPDATE` issued
*outside* the event, because a repeat call takes `RecordEvent`'s already-recorded
path and skips apply entirely — and because a heartbeat is not an event worth
keeping.

**`EndAgentSession(ctx, taskID, actorID, agent, sessionID, usage)`** — records
an `agent_session.ended` event, sets `ended_at`, and writes any token/cost
values supplied by the caller.

HTTP surface, in the existing lifecycle group:

- `POST /api/v1/tasks/{id}/agent-session` — body `{agent, agent_version, session_id}`
- `POST /api/v1/tasks/{id}/agent-session/end` — body `{agent, session_id, input_tokens?, output_tokens?, cost_amount?, cost_currency?}`

Everything is named `agent-session`, never `session`: `internal/api/session.go`
already owns "session" for web and CLI auth.

## Hook wiring

`hookrun.Payload.SessionID` already carries the value. `lode hook` gains three
events (`heartbeat`, `worktree-enter`, `worktree-exit`) and `session-end` gains
a backbone call it does not make today:

| `lode hook` event | Call | Session id from | Claude Code binding |
|---|---|---|---|
| `session-start` | `TouchAgentSession` after `ensureLease` | payload | `SessionStart` |
| `heartbeat` | `TouchAgentSession` | payload | `Stop`, `StopFailure`, `SubagentStop`, `Notification` |
| `worktree-enter` | `TouchAgentSession` on the entered worktree's lease | payload | `PostToolUse` matcher `EnterWorktree` |
| `worktree-exit` | `EndAgentSession` for the exited lease's row | payload | `PostToolUse` matcher `ExitWorktree` |
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

**Volume control.** These bindings fire more often than `Stop` alone, so the
heartbeat is debounced client-side: `worklode-session.json` gains a
`last_heartbeat_at` field, and a heartbeat within 60s of the recorded one is
skipped without a backbone call. `worktree-enter` / `worktree-exit` are not
debounced — they carry a lease change, not just liveness.

**Not bound, and why:**

- `PostToolUse` *unmatched*, and `PostToolBatch` — hundreds of firings per
  session against a 10s default timeout, for no signal `Stop` lacks. Matched
  `PostToolUse` is a different matter and is used above: the matcher is a tool
  name, so binding `EnterWorktree` / `ExitWorktree` costs nothing per ordinary
  tool call.
- `UserPromptSubmit` — a strict subset of `Stop`, missing autonomous turns.
- `WorktreeCreate` / `WorktreeRemove` — already handled. `handleWorktreeCreate`
  runs before any session exists in that worktree, and `handleWorktreeRemove`
  releases the lease, which stamps `ended_at` on every open session through
  `closeLease`.
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

## Error handling

- An unreachable or erroring backbone degrades to a stderr warning; the coding
  session proceeds unaffected.
- A heartbeat for a task the caller no longer holds returns `ErrNotFound` and
  is warned, not retried. Losing a lease means the session is no longer
  authoritative.
- Repeat heartbeats are idempotent by construction (deterministic event
  external id + `ON CONFLICT DO NOTHING`).

## Testing

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

## Out of scope

- Computing tokens and cost. The numbers are only reachable by parsing Claude
  Code's transcript JSONL, which is tool-specific; the columns wait. The
  `Stop` and `SessionEnd` payloads both carry `transcript_path`, so the hooks
  wired here are already standing where that work will go.
- A `lode sessions` listing command and any web UI surface.
