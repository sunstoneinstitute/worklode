---
status: draft
issued: 2026-08-22
requires:
- 012-agent-sessions.md
amends:
  "#sec-1":
  - 012-agent-sessions.md#sec-1
  "#sec-2":
  - 012-agent-sessions.md#sec-3
  "#sec-3":
  - 012-agent-sessions.md#sec-4
---
# Spec 052 — Project overhead cost

## 0. Problem {#sec-0}

Every hook handler that reports agent-session usage —
`handleSessionEnd`, `handleHeartbeat`, `handleWorktreeEnter`, and the
`reportSession`/`endSession` helpers they call (`internal/hookrun/hookrun.go`)
— is gated by `leasedWorktree(l, dir)`, where `dir` is the hook process's own
working directory at invocation time. When `dir` is not a task worktree
(no lease is stamped there), the handler returns immediately: no report is
attempted, not even for the tokens the transcript already has.

That gate assumes one coding-agent session runs in exactly one task worktree
for its whole life. The dominant real usage pattern violates it: a
long-running orchestrator session (subagent-driven development) runs from a
repository's **main checkout**, and dispatches subagents into individual task
worktrees via the Agent/Task tool. Claude Code logs a dispatched subagent's
turns into the **orchestrator's own transcript** as sidechain entries, tagged
with whatever working directory that dispatch actually ran in — there is no
second Claude Code process, no second `session_id`, and therefore no second
set of hook events. Every hook event for the whole session — `session-start`,
`heartbeat`, `session-end` — fires with `dir` equal to the main checkout, and
every one of them is gated out today. `internal/transcript/transcript.go`'s
token-bucketing by working directory is correct; the bug is that reporting
never runs at all for a session whose own `dir` is the main checkout, so
neither the orchestrator's own turns nor the correctly-tagged subagent turns
in its transcript ever reach `replaceSessionUsageTx`.

Verified against production data (`worklode.dev.sunstoneinstitute.ai`,
compared to a raw scan of `~/.claude/projects/*/*.jsonl` transcripts on the
host running these sessions, 9-day window): Worklode's tracked total across
every project for the window was ~$152. Four representative large transcripts
from the busiest two days alone carried 176M tokens under a main-checkout
`cwd` against 14M tokens under a worktree `cwd` — over 90% of real spend
invisible to the tracker, on top of a pre-existing 10-30x undercount even on
days that did report something. The days where tracked cost diverges most
from the ground-truth scan are exactly the days orchestrator-style sessions
did the most work.

**Decision.** Reporting runs unconditionally on every heartbeat and
session-end, parsing the whole transcript rather than filtering by the
hook's own directory, and splits the result two ways: tokens whose
transcript-recorded `cwd` resolves to a task worktree this actor currently
holds the lease on bill to that task exactly as today; every other token —
main-checkout turns, a worktree whose lease this actor no longer holds, or a
directory outside the repo's configured worktree layout — bills to a new
project-level **overhead** bucket. Overhead is a new cost category attached
to a project, never folded into any one task's numbers and never silently
dropped.

## 1. Schema {#sec-1}

> **Amends spec 012 §1.** Overhead usage needs no lease and no task, so it
> cannot live in `agent_sessions`/`agent_session_usage` (`lease_id NOT NULL`).
> Two new tables carry it, mirroring migration `0008_session_cost`'s shape.

Migration `0046_project_overhead_usage`:

```sql
-- One row per (project, agent, external session id, day, model, speed).
-- Mirrors agent_session_usage's shape, but keyed to a project directly:
-- overhead usage has no lease to hang off (a main-checkout session holds no
-- task's lease at report time), so there is no agent_sessions row for it.
--
-- Replaced wholesale per (project, agent, external session id), never
-- incremented — same reason as agent_session_usage: the source transcript is
-- cumulative, so a report carries an absolute total that must overwrite a
-- prior one, not add to it.
CREATE TABLE project_overhead_usage (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent                 text NOT NULL,
    external_session_id   text NOT NULL,
    usage_day             date NOT NULL,
    model                 text NOT NULL,
    speed                 text NOT NULL DEFAULT 'standard',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    -- NULL means "no price on file for this model on this day" — see
    -- agent_session_usage.cost_amount's identical comment.
    cost_amount           numeric(14,6),
    cost_currency         text NOT NULL DEFAULT 'USD',
    PRIMARY KEY (project_id, agent, external_session_id, usage_day, model, speed),
    CONSTRAINT project_overhead_usage_speed_known CHECK (speed IN ('standard', 'fast')),
    CONSTRAINT project_overhead_usage_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT project_overhead_usage_nonnegative CHECK (
        input_tokens >= 0 AND cache_write_5m_tokens >= 0 AND
        cache_write_1h_tokens >= 0 AND cache_read_tokens >= 0 AND
        output_tokens >= 0)
);

-- Supports the per-project overhead rollup recompute, which filters by day.
CREATE INDEX project_overhead_usage_day ON project_overhead_usage (usage_day);

-- Derived rollup, recomputed from scratch for the affected (project, day)
-- pairs whenever a (project, agent, session) overhead report is replaced —
-- same discipline as project_daily_cost, and deliberately its own table
-- rather than new columns there: project_daily_cost's rows are, by
-- construction, exactly what agent_session_usage sums up through the
-- lease -> task chain, and overhead has no task to join through. Keeping it
-- separate means neither rollup can silently absorb the other's rows, and a
-- reader can tell task-attributed spend from overhead apart at the storage
-- layer, not just by convention.
CREATE TABLE project_daily_overhead_cost (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    usage_day             date NOT NULL,
    cost_currency         text NOT NULL DEFAULT 'USD',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    cost_amount           numeric(14,6) NOT NULL DEFAULT 0,
    unpriced_tokens       bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, usage_day, cost_currency),
    CONSTRAINT project_daily_overhead_cost_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$')
);
```

`model_prices` is unchanged and shared: overhead buckets are priced the same
way task-attributed ones are (`modelPriceFor`), by model/speed/day, since a
token costs what it costs regardless of which bucket it lands in.

## 2. Store and API {#sec-2}

> **Amends spec 012 §3.** A third store entry point alongside
> `TouchAgentSession`/`EndAgentSession`, with its own rollup, but no lease
> holder check — overhead has no lease to check a holder against.

**`(*Store) ReportProjectOverheadUsage(ctx, projectID, agent, externalSessionID string, buckets []SessionUsageBucket) error`**
— replaces `agent`/`externalSessionID`'s complete overhead usage for
`projectID`, the same replace-not-accumulate contract as
`replaceSessionUsageTx`, and rebuilds `project_daily_overhead_cost` for every
affected day inside the same transaction. Validates `agent` against the same
vocabulary `TouchAgentSession` does and rejects an empty
`externalSessionID`, both `ErrInvalidInput`; an unknown `projectID` is
`ErrNotFound`. There is no holder check: nothing owns a lease here to hold,
and any authenticated actor may report overhead for any project it can name
— the same breadth already accepted for `permCrewWrite` (023 §... — see
`internal/api/authz.go`'s comment on that grant).

HTTP surface, a new project-scoped route:

- `POST /api/v1/projects/{id}/overhead-usage` — body
  `{agent, external_session_id, usage}`, all required. `usage` is the same
  `SessionUsageBucket` shape `agent-session`/`agent-session/end` take.
  204 on success. Guarded by a new permission, `project.report`
  (`permProjectReport`), granted to every authenticated role — reporting
  overhead is not a claim on any one task, so it does not belong under
  `task.claim`.

**`ProjectCost` becomes a combined report.** `(*Store) ProjectCost` now reads
`project_daily_cost` and `project_daily_overhead_cost` for the window and
full-outer-joins them per `(usage_day, cost_currency)`, so a day with only
overhead spend (no task-attributed usage at all — the common case for a pure
orchestration day) still produces a row. Each `CostDay`/`CostTotal`'s
top-level `Tokens`/`Cost`/`UnpricedTokens` become the **combined** total
(task-attributed + overhead) — this is "how much did this project spend,"
which is the number the bug in §0 made wrong — and two new fields,
`OverheadTokens`/`OverheadCost`/`OverheadUnpricedTokens`, carry overhead's own
share of that total, so it is visible rather than merged away.
`TaskCost` is **not changed**: overhead is by construction not attached to a
task, so a task's cost report has nothing to combine — its
`OverheadTokens`/`OverheadCost`/`OverheadUnpricedTokens` are always zero.

## 3. Hook wiring {#sec-3}

> **Amends spec 012 §4.** The `leasedWorktree` guard on the usage-reporting
> handlers changes from "reject unless `dir` is a task worktree" to "reject
> unless `dir` is inside a git worktree at all" — main-checkout and unleased
> worktrees now proceed, reporting against no task id. The lease-touching
> handlers (`ensureLease`, `RenewLease`, `session-start`'s brief fetch,
> `worktree-create`/`worktree-remove`, `offerScan`) keep the original,
> task-worktree-only gate unchanged.

**`transcript.Bucket` gains a `Cwd string` field**, and the bucketing key
(`day, model, speed`) gains `cwd` as a fourth component, unconditionally —
`Options.Root`'s filtering behavior is unchanged (it still drops entries
outside `Root`; kept entries now also carry their own `Cwd`). This is
backward compatible: a caller using `Options{Root: root}` for a single
worktree, as every existing call site does, gets the same tokens split into
finer-grained rows sharing one `Cwd`, which the server's own
`mergeUsageBuckets` already re-merges by `(day, model, speed)` — no existing
caller's totals change.

**`classifyTranscriptUsage(opts, l, transcriptPath) map[string][]model.SessionUsageBucket`**
is a new `internal/hookrun` function: it parses `transcriptPath` with no
`Root` filter (every `cwd` the session touched, not just the calling
handler's own worktree) and groups the buckets by task id, resolved per
distinct `cwd` via `l.TaskID(cwd)` — the same worktree-layout resolution the
hook guard itself uses, so a `cwd` outside the configured worktree base, or
one whose directory name/stamp carries no task id, groups under the empty
string. A missing transcript, an unreadable file, or an empty result all
yield `nil` — the same no-failure contract `sessionUsage` already has, which
this supplements rather than replaces (`sessionUsage`, and every existing
`Options{Root: root}` call site, is untouched).

**`reportSession`/`endSession` are extended, not replaced.** Each still
reports its own handler's task (when it has one) exactly as before —
`TouchAgentSession`/`EndAgentSession` with that task's own transcript slice —
and then hands every *other* group `classifyTranscriptUsage` found to a new
`reportOtherTaskAndOverheadUsage(ctx, opts, c, root, sessionID, byTask)`:
a real task id is reported through `TouchAgentSession` (never
`EndAgentSession` — this call has no opinion on whether that other task's own
session should end, only that these tokens billed to it); a `TouchAgentSession`
failure for that task (most commonly `ErrNotFound` — this actor no longer
holds that task's lease) redirects those buckets to overhead instead of
dropping them; the empty-string group and every redirected group are reported
together via a new `reportOverhead(ctx, opts, c, root, sessionID, buckets)`,
which resolves the current repo's project from local config only
(`cli.CurrentProjectFrom(root)` — a new function mirroring
`cli.WorktreeDirFrom`'s contract: repo-local config, then user config, no
keychain, no server round trip) and calls the new
`POST /api/v1/projects/{id}/overhead-usage`. No project configured degrades
to a warning and the buckets are dropped — the same "never fail the
triggering event" contract every hook call already has, applied to a
genuinely unconfigured repo rather than a transient error.

`handleHeartbeat`, `handleSessionEnd`, and `handleWorktreeEnter` change their
guard from `leasedWorktree`'s `ok` (which requires a task id) to `root != ""`
(inside a git worktree at all — `worktree.Root(dir)`, still resolved via
`leasedWorktree`, whose `root` return is used regardless of `ok`). `taskID`
may now be `""`; `reportSession`/`endSession` treat that as "no own task to
report against" and skip straight to `reportOtherTaskAndOverheadUsage`. The
session marker (debounce, liveness) is unaffected: it is written and read
against `root` exactly as before, and a main checkout gets its own marker
file the same way a task worktree does — nothing about the marker mechanism
assumed a task id.

**Unchanged, deliberately:**

- `handleWorktreeExit` keeps its `ok`-gated guard. `ExitWorktree`'s tool input
  carries an explicit path with no cwd fallback (012 §4's existing rule), and
  that path is, by construction, a worktree being left — there is no
  main-checkout analogue to extend this handler to. It still benefits from
  the wider classification inside `endSession`, since that function's own
  change is shared.
- `handleSessionStart`'s lease-touch/brief-fetch side (`ensureLease`,
  `fetchBrief`, `ensureSkills`) stays gated on a real task id — nothing to
  renew or inject without one — and its call to `reportSession` is
  reached only inside that same gate, so session-start does not report
  overhead. This loses nothing: `heartbeat` fires every turn and
  `session-end` reports the full transcript, so between them every token a
  session bills is eventually reported even though session-start itself
  never opportunistically catches up a stale main-checkout report the way it
  does for a task worktree (there is no prior `agent_sessions` row for a
  main-checkout session to catch up in the first place).
- `handlePreCommit` stays fully lease-gated: a git `pre-commit` hook fires
  inside the worktree being committed to, and subagent-driven development's
  orchestrator does not commit from the main checkout — its dispatched
  subagents commit from their own task worktrees, each with its own
  `pre-commit` invocation, already correctly gated. Its `reportSession` call
  also always passes an empty transcript path, so this handler's behavior is
  unchanged either way.
- `offerScan`, `handleWorktreeCreate`, `handleWorktreeRemove` are about lease
  liveness and lifecycle, not usage reporting, and are untouched.

## 4. Wire model and cockpit {#sec-4}

`model.CostDay`/`model.CostTotals` (`internal/model/cost.go`) each gain an
`Overhead CostOverhead` field, a new nested type:

```go
type CostOverhead struct {
	TokenCounts
	CostAmount     string `json:"cost_amount"`
	UnpricedTokens int64  `json:"unpriced_tokens"`
}
```

nested under `"overhead"` on the wire rather than flattened, so its
`input_tokens`/`cost_amount`/etc. keys cannot collide with the combined
totals' own. `model.ProjectOverheadUsageInput` (request body for the new
endpoint) carries `Agent`, `ExternalSessionID`, `Usage []SessionUsageBucket`.

The cockpit's automation-boundary card (`internal/ui/cockpit.templ`,
`automationBoundary`) shows the combined "Agent spend, 30 days" figure it
already shows today — now correct, since `ProjectCost`'s combined total
includes overhead — plus one added line breaking out the overhead share, so
a reader can see both how much a project spent and how much of that was
orchestration overhead rather than task work. `ui.CockpitCostTotal` gains an
`OverheadCostAmount string` field to carry it.

## 5. Testing {#sec-5}

- `transcript`: a fixture with two distinct `cwd` values produces buckets
  carrying each of them, under both an empty `Options.Root` and a `Root` that
  matches one of them (existing filtering behavior unchanged; grouping now
  splits by `cwd` too).
- Store: `ReportProjectOverheadUsage` replaces rather than accumulates a
  repeat report for the same `(project, agent, external session id)`;
  rejects an unknown agent and an empty session id; 404s an unknown project.
  `ProjectCost` combines task-attributed and overhead rows for the same day,
  including a day with overhead only. `TaskCost` is unchanged by a project
  that also has overhead recorded (its report carries none).
- API: happy path, 400 for a malformed body, 404 for an unknown project, on
  `POST /api/v1/projects/{id}/overhead-usage`. The route-guard boot check
  (`internal/api`'s `NewServer` table test) covers the new route by
  construction once it is registered.
- `hookrun`: a heartbeat fired from the main checkout, with a transcript
  containing one cwd under a currently lease-held task worktree and one cwd
  with no matching task, reports the first through the ordinary
  `agent-session` endpoint and the second through
  `overhead-usage`; a heartbeat whose transcript names a task this actor no
  longer holds the lease on (a `TouchAgentSession` 404) falls back to
  overhead for that group instead of dropping it; `TestHeartbeatOutsideWorktreeIsNOP`
  (a directory with no enclosing git repository at all) is unchanged, since
  `root == ""` is still a hard NOP.

## 6. Out of scope {#sec-6}

- Currency conversion between a project's task-attributed and overhead
  totals — both are already summed only within one currency (spec 012 §1),
  and this does not change that.
- Any change to `Store.TaskCost`'s query path or its `leases` scan. The
  known gap noted against it (`docs/follow-ups.md`, "From WL-148 — `lode task
  cost`": no plain index on `leases.task_id`) is neither better nor worse
  after this spec — overhead usage never touches that query.
- A cockpit or CLI surface for overhead by itself (e.g. "which sessions
  produced the most overhead") — only the existing project cost surfaces
  gain the breakdown described in §4.
- Extending `handleSessionStart`'s or `handlePreCommit`'s guard to report
  overhead — see the rationale in §3's "Unchanged, deliberately" list.
