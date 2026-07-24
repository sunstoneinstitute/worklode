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
    cost_usd            numeric(12,6),
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
- `POST /api/v1/tasks/{id}/agent-session/end` — body `{agent, session_id, input_tokens?, output_tokens?, cost_usd?}`

Everything is named `agent-session`, never `session`: `internal/api/session.go`
already owns "session" for web and CLI auth.

## Hook wiring

`hookrun.Payload.SessionID` already carries the value. Three touchpoints:

| Hook | Call | Session id from |
|---|---|---|
| `session-start` | `TouchAgentSession` after `ensureLease` | payload |
| `pre-commit` | `TouchAgentSession` alongside the existing `RenewLease` | marker file |
| `session-end` | `EndAgentSession` before removing the marker | payload |

`handleSessionEnd` makes no backbone call today; it gains one. A git
`pre-commit` hook has no stdin, so it reads the session id out of the local
`worklode-session.json` marker — which is what keeps `last_seen_at` meaningful
across a long session rather than only at its start. A marker that is missing
or stale means no heartbeat, not an error.

The marker file otherwise stays as-is: it reports process liveness on one
machine, which the backbone cannot.

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
- `hookrun` tests: session-start reports the session, session-end closes it,
  and a failing backbone still exits 0.

## Out of scope

- Computing tokens and cost. The numbers are only reachable by parsing Claude
  Code's transcript JSONL, which is tool-specific; the columns wait.
- A `lode sessions` listing command and any web UI surface.
