---
status: draft
covers: NO-SPEC
---
# Steering a running supervisor session

Design record of what shipped: a way for `lode-server` to push a short
steering message into a *live* Claude Code session it does not run and
cannot reach as a server. No spec governs this yet (`NO-SPEC`) — it is small
enough, and new enough, that a spec would be premature.

## Why not the obvious mechanisms

The intended shape is a Sonnet Claude Code session supervising a
`lode-worker` subagent loop, staying mostly idle itself, with a central
control plane — `lode-server`, running multi-host in Kubernetes with no fixed
egress IP — occasionally injecting a short instruction into that session.

Two mechanisms were ruled out before landing on this one:

- **Cross-session messaging** (Claude Code's built-in per-session Unix
  socket) is same-machine only. It cannot reach a session from a multi-host
  control plane.
- **A remote/HTTP MCP server hosted by `lode-server` directly** cannot be a
  `claude/channel`: the capability requires a local stdio MCP server spawned
  as a child process of the Claude Code session itself. A URL-based MCP
  server is never offered that role.

So the piece that actually works is a small **local** process that holds the
stdio connection to Claude Code and bridges it to `lode-server` over ordinary
HTTPS. That process is `lode channel serve`.

## `task_instructions` (migration 0052)

```sql
CREATE TABLE task_instructions (
    id           bigserial PRIMARY KEY,
    task_id      text NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    body         text NOT NULL,
    created_by   text REFERENCES actors (id),
    created_at   timestamptz NOT NULL,
    delivered_at timestamptz
);
CREATE INDEX task_instructions_pending ON task_instructions (task_id, id)
    WHERE delivered_at IS NULL;
```

A row is queued **task-addressed** — the control plane still says "steer task
WL-309" — and stays pending until a claim stamps `delivered_at`. `store.
EnqueueInstruction` records a `task.instructed` event (the write is an
operator act worth an audit trail); `store.ClaimPendingInstructionsForActor`
does not record one — `delivered_at` is itself the durable record, and an
event per 3-second poll would be pure log noise. Both are counted by
`worklode_task_instructions_total{op,outcome}`, and a successful claim also
adds to `worklode_task_instructions_delivered_total`; the two together answer
"is the relay dead" in PromQL without a separate backlog gauge.

Because `lode-server` is authoritative over what is undelivered, a relay
restart or a network blip during a node reschedule never loses an
instruction — the row is just still pending on the next poll. Delivery is
at-least-once, not exactly-once, which is fine for short human-paced
steering text.

## The two routes, and the authorization shape

```
POST /api/v1/tasks/{id}/instructions   guarded(permTaskWrite)
POST /api/v1/instructions/claim        guarded(permTaskClaim)
```

Enqueue is task-addressed and ordinary: an operator (or another service)
writes a steering message onto a specific task.

Claim is different in kind, not just in path shape: it carries **no task id**
at all. `ClaimPendingInstructionsForActor` scopes purely by actor — "every
pending instruction on any task this actor currently leases" — because the
addressing this whole feature needs is actor-scoped, not task-scoped. The
supervisor session runs in the main repo root, not inside a worker's task
worktree, so there is no single task id to resolve the way `lode-hook`
resolves one from the worktree it runs in. This also means `.mcp.json` needs
no per-task configuration: the same channel process works unmodified
regardless of which task the worker is currently on.

That actor-wide scope is also exactly why the claim route must plainly deny
a task-scoped token. It shipped once as `guardedAny(permTaskClaim)` — the
router's existing helper for an actor-wide route that carries no `{id}` — and
that was a real security hole: a task-scoped token (001 §2.1) minted for one
task's worktree could call the route and drain instructions queued against a
*different* task the same actor also leases, because an actor can hold
concurrent leases (migration 0016) and the handler has nothing scoping by the
calling token's bound task. The fix reverted the route to plain
`guarded(permTaskClaim)`, which denies a task-scoped token outright — only a
full actor-identity token (`lode login`) may claim. `TestTaskTokenScope`
pins the refusal on both routes. The enqueue route was never at risk the same
way: it was `guarded(permTaskWrite)` from the start, so a task-scoped token
cannot message a task's own lease holder either.

## `lode channel serve`

The stdio MCP relay Claude Code spawns as a child process. It exists because
of the constraint above: `claude/channel` notifications require a local
stdio server, and a multi-host control plane cannot itself be that server.
`lode channel serve` bridges the two — it holds the stdio connection to
Claude Code locally, and separately polls `lode-server` over ordinary HTTPS
(the same `wl_` bearer token `lode login` already mints) to atomically claim
pending instructions for whatever tasks the caller's actor currently leases.

It speaks just enough of the legacy MCP stdio protocol to stay a channel —
roughly 120 lines, no MCP SDK dependency:

- `initialize` — echoes the caller's `protocolVersion` back verbatim. This
  is load-bearing: Claude Code only keeps the unsolicited-notification path
  open on the legacy protocol era, and negotiating into a newer revision
  silently closes it.
- `notifications/initialized` — a notification, no reply.
- `tools/list` — replies with an empty tool list.
- everything else, **including `server/discover`**, is left unimplemented
  and answered with JSON-RPC error `-32601`. Answering `server/discover` at
  all is what would negotiate Claude Code into the newer, channel-incompatible
  protocol era, so it must stay unanswered.

A poll goroutine (`--interval`, default 3s) waits for `initialize` to
complete before claiming anything — a claim stamps `delivered_at` in the same
query that reads the row, so a notification sent before Claude Code is ready
to receive one would be a permanently lost instruction, not a delayed one.
Each claimed instruction becomes one unsolicited
`notifications/claude/channel` line:

```json
{"jsonrpc":"2.0","method":"notifications/claude/channel","params":{"content":"<body>","meta":{"task":"<task>","instruction_id":"<id>","from":"<created_by>"}}}
```

Meta keys are restricted to `[A-Za-z0-9_]+` — Claude Code silently drops any
key that doesn't match, which is why `instruction_id` and not
`instruction-id`. Only stdout carries JSON-RPC; all logging goes to stderr,
including a failed claim, which is otherwise ignored (the row stays pending
server-side, and the next tick retries).

## `lode task instruct <id> <message>`

The write side an operator actually types: `lode task instruct WL-309 "check
the logs before continuing"` resolves the task id and calls the enqueue
route, then renders the queued instruction the same way every other `lode
task` mutation does (`cli.InstructionTable`, `--json` for the raw response).
It lives in its own file, `internal/cmd/taskinstruct.go` — `instructions.go`
already exists in `internal/cmd` for the unrelated `AGENTS.md`/
`CLAUDE.local.md` file management, and the two concepts only share a name.

## Wiring a session up today is manual

Nothing in `lode install` sets up a session to receive steering instructions
yet. Doing so today means adding a `.mcp.json` entry by hand:

```json
{ "mcpServers": { "lode": { "command": "lode", "args": ["channel", "serve"] } } }
```

and launching Claude Code with two flags that opt into the legacy channel
path:

```
claude --channels server:lode --dangerously-load-development-channels
```

There is no automation for either step, and that is deliberate for now
rather than an oversight: `lode install` wires files a session reads
(git hooks, agent settings, skill links), but nothing in worklode's own
tooling is the thing that *spawns* `claude` in the first place — there is no
existing launch point in this repo to hook the `.mcp.json` write or the
launch flags into. Automating this is real follow-up work, gated on that
launch point existing (or being decided) rather than on anything in this
plan.
