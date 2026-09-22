# Agent harness and sessions

This document describes how a coding agent enters, holds, and leaves Worklode work. Coordination is done by deterministic machinery (compiled hooks, a server-side claim, a worktree-bound lease) so the model spends tokens only on judgment. It covers the worktree and branch naming that the lifecycle keys off, the `lode-hook` and `lode-statusline` executables, the harness adapters `lode install` drives, the `agent_sessions` record of who worked what and when, the Pi extension, the reconciliation and diagnosis commands, and the inbox import that onboards a repo with existing history. Command spellings follow the naming law in 09-cli-and-skills.md; the six executables and their dependency boundaries are defined in 01-system-and-deployment.md.

## 1. Design lens

Three rules hold throughout.

| Rule | Meaning |
|---|---|
| CLI over MCP | Agents drive `lode --json`. No MCP server, no per-tool schema tokens in context. An MCP shim for clients that cannot drive a CLI is deferred and would wrap the same commands. |
| Hooks over prompts | Lease renewal, resume, and release are compiled hooks firing on editor and git events. The model is never asked to remember a heartbeat. |
| Server-side selection over agent reasoning | `lode task claim --next` ranks and leases in one transaction (03-tasks-and-execution.md). The agent never lists, picks, then claims. `lode task claim <id>` claims a specific task. |

Machinery does acquire, bind, renew, resume, release, and context assembly. The model does done/block/release judgment and design authoring. Product is Worklode, CLI is `lode`, slash commands are `/lode:<name>`.

## 2. Worktree-bound lease lifecycle

The worktree is the unit of Worklode work. The lease binds to the git worktree, never to the session. A plain session in a normal checkout never touches Worklode; the only entry is a claim, which creates a worktree.

A claimed task's worktree is `<git-root>/<worktree_dir>/<branch>`, by default `.worktrees/<id>-<slug>`. The path is a pure function of the task, derived after the lease is held, so any hook can map worktree to task to lease with a path parse.

| Phase | What happens |
|---|---|
| Acquire | `lode work next` (shortcut `lode next`, or `/lode:next`) runs the atomic `claim --next`, creates the worktree, binds the lease to it (the backbone records the worktree identity on the lease), and injects `lode task brief`. |
| Hold | The lease lives while the worktree exists. Sessions open and close around it. Renewal is the commit-cadence heartbeat, not a session timer. |
| Resume | A lease can expire when the sweeper reclaims a stalled worktree. Because the worktree still exists under a deterministic name, `lode work resume` or the `SessionStart` / `EnterWorktree` hooks re-acquire that same task's lease. Re-acquiring your own worktree is safe and distinct from auto-claim, which never happens. |
| Release | `ExitWorktree` or worktree removal auto-releases. `lode work submit` and `lode work block` release explicitly. |

Guard invariant: no bound worktree, no Worklode behavior. Every hook keys off the path guard in §3.4 and the backbone's worktree-to-lease binding.

## 3. Branch names and worktree layout

### 3.1 Server-rendered branch template

A branch name is rendered from `LODE_BRANCH_TEMPLATE`, a Go `text/template` that produces the whole name. Default `{{ .id }}-{{ .slug }}`, yielding `WL-3-fix-the-thing`. The server is the sole authority: it renders once per claim and returns the result. The CLI's only fallback, when a response carries no branch, is the literal `<id>-<slug>`.

| Field | Value |
|---|---|
| `.id` | task id, e.g. `WL-3` |
| `.slug` | slugified task title |
| `.projectId` | project id the task belongs to, slugified |
| `.kind` | task kind, slugified |

Rendering uses `missingkey=error`. There is no bare `.project`. `.projectId` and `.kind` pass through the same slugify rule as `.slug`, so any template yields a legal ref.

Validation runs at `lode-server` startup and a bad template fails startup. Three conditions: it parses and renders against sample values; it references `.id` (otherwise no branch could be correlated back to a task); the result is a legal git ref (non-empty, no control characters, none of space `~^:?*[\`, no `..`, `//`, leading or trailing `/`, `@{`, and no path component starting with `.` or ending in `.` or `.lock`). The check is in Go; the server needs no git binary.

### 3.2 Reverse parsing: branch to task id

`store.TaskIDFromRef` maps a pushed branch or PR head ref back to a task id using a pattern derived from the same template, built once beside it: render with sentinels per field, `regexp.QuoteMeta` the result, substitute `.id` with `([A-Z][A-Z0-9]*-[0-9]+)` and every other field with `[^/]*`, anchor with `^`/`$`. The default template yields `^([A-Z][A-Z0-9]*-[0-9]+)-[^/]*$`. Every correlation path gates on `taskExists` before writing a binding, so a shape match on a non-existent id is dropped. Two accepted consequences: a bare `WL-3` branch does not correlate under the default template, and a template that renders `.id` adjacent to another field is ambiguous and is not prevented.

`store.TaskIDFromBody` correlates on a `Worklode-Task: <id>` line in a PR body regardless of branch shape.

### 3.3 Base directory

| Setting | Where | Default |
|---|---|---|
| `LODE_BRANCH_TEMPLATE` | server env | `{{ .id }}-{{ .slug }}` |
| `worktree_dir` | repo `.worklode/config.toml` | `.worktrees` |
| `LODE_WORKTREE_DIR` | client env, overrides `worktree_dir` | none |

A branch name is published to GitHub and CI, so the backbone owns it. A worktree path is local to one checkout, so the checkout owns it. `worktree_dir` is interpreted relative to the git root; absolute paths and `..` escapes are rejected. Because `.worklode/config.toml` is repo content it is checked out inside every worktree, so a hook resolves the base from its own cwd. `LODE_WORKTREE_DIR` does not persist; a worktree created under it is invisible to a later session started without it, and every hook NOPs silently.

The layout is flat: every worktree is exactly one directory below the base, named by the branch. A `/` in a rendered branch is flattened to `-` in the directory name (`team/WL-3-x` becomes `.worktrees/team-WL-3-x`). `.gitignore` carries `.worktrees/`.

### 3.4 The path guard

The guard asks one string question: is this path exactly one segment below a segment equal to the configured base directory? No template, no config beyond the base, no subprocess, no network. It runs on every hook event, most of which are nowhere near a worktree. A path inside a worktree is not itself a worktree root; handlers that accept one resolve it through `worktree.Root` first.

Id resolution is the second, separate question, paid only by events that cleared the guard: `git config --worktree --get worklode.task-id`, which `lode work next` stamps at creation, falling back to the first `[A-Z][A-Z0-9]*-[0-9]+` substring in the segment. The stamp needs `extensions.worktreeConfig` in the repo's local git config; `lode install` and `lode work next` both enable it. A stamped worktree outside the base is still invisible.

Scanning for adoptable worktrees (`SessionStart` outside a worktree) reads the base one level deep.

### 3.5 Moving a worktree

`git worktree move` and `git branch -m` alone do not make a moved worktree usable: the lease still names the old `<host>:<path>` identity and `cli.ReacquireOrRenew` refuses to resume a lease held by a different worktree. No command rebinds an existing lease; `RebindWorktree` runs only from `lode work next`. The sequence is: move, rename the branch, `lode task release <id>`, then `lode work resume <new>`.

## 4. Hooks

Every lease-bearing hook is a NOP outside a Worklode worktree: absent guard, `exit 0` immediately. The guard is per handler. Event names below are Claude Code's; §5.4 maps them per harness.

| Event | Action | Guard |
|---|---|---|
| `EnterWorktree` | Auto-resume: re-acquire the bound lease | In a Worklode worktree, no running session for it, lease expired |
| `SessionStart` (in worktree) | Resume: inject `lode task brief <id> --json`, re-acquire lease if expired | In a Worklode worktree. Never a silent claim. |
| `SessionStart` (outside worktree) | Offer to resume an abandoned worktree | Compiled scan finds a worktree with a bound, expired lease and no running session. Offer only, script-level prompt at most. |
| `PreToolUse` on `git commit` | `lode task renew` (heartbeat) | NOP when not on a Worklode task |
| `ExitWorktree` / `SessionEnd` | Release the lease if idle | In a Worklode worktree with a held lease |
| `commit-msg` (git) | Stamp `Worklode-Task: <id>` into the message | In a Worklode worktree, no merge in progress, message has a body. Takes no lease and makes no backbone call; the id comes from `worklode.task-id` on disk. |
| `post-merge`, `post-commit` (git) | Report the merge: name the tasks whose branches this commit brought in | HEAD is the repo's default branch. NOP before any network call otherwise. |

Notes:

- Inside a worktree, entering or starting re-acquires the bound lease automatically. Outside, the session only gets an offer. Acquisition stays deliberate.
- Renewal is a heartbeat driven by commits. A stalled session stops committing and its lease ages out to the sweeper.
- `commit-msg` rather than `prepare-commit-msg`, so the "only stamp a message that already has a body" gate preserves the empty-message abort for interactive commits.
- Both `post-merge` and `post-commit` are bound because `git merge` fires the first while a squash merge or a merge-resolving commit fires only the second.
- Merge reporting is a probe. The client cannot reverse a branch to an id (that is the server's job), so candidates come from the backbone (open tasks of this repo's project, each with its rendered branch) and the client answers "is this branch's work in HEAD now, and was it not in the previous commit?". Ancestry answers a true merge or fast-forward; a patch-id walk answers a squash. Rebase is out of scope. The "not in the previous commit" half is required because `lode work next` branches at the default tip, so every idle branch is already an ancestor of HEAD.
- A merge whose branch is already gone falls back to the webhook. A report is never guessed from a missing branch.

### 4.1 Implementation and coexistence

- Hooks are the compiled `lode-hook` executable. It takes an event and its arguments, `--harness <id>`, `--next <cmd> [argv...]`, or `--list`. It does not import Cobra or the command tree and parses arguments with the standard library.
- Daisy-chain: instead of `exit(0)`, a hook `execve`s the `--next` command, passing the payload through. Words between the event and `--next` are the hook's own; everything after `--next` goes to the chained hook verbatim. `commit-msg` names `"$@"` on both sides so neither half loses git's message-file argument.
- `lode install` wires `pre-commit`, `commit-msg`, `post-merge`, and `post-commit` as git hooks, editor-agnostic. An existing hook is preserved as `<name>.pre-lode` and invoked through the chain. If `.pre-commit-config.yaml` exists, `pre-commit` always chains to the framework (that hook only). The installer is idempotent: it detects and re-points its own link.
- Two standing rules for every hook: every backbone call runs under the 2s `backboneTimeout`, and no hook ever fails its triggering event. Backbone errors degrade to a stderr warning.
- Agent identity comes from `LODE_AGENT` (default `claude-code`), an environment variable rather than a flag because flag parsing is disabled so `--next` argv passes through verbatim.

## 5. Harness adapters and `lode install`

One coordination model, N harnesses. Every harness reads the same `SKILL.md`, fires the same lifecycle moments, and differs only in which directory it reads skills from and which config file names the hook. The `internal/harness` package holds one adapter per harness plus a registry keyed by id: `claude-code`, `codex`, `copilot`, `cursor`, `amp`, `opencode`, `pi`.

```go
type Harness interface {
    ID() string
    Detect(repoDir string) (bool, error)
    SkillTargets(repoDir, scope string) ([]string, error)
    InstallHooks(repoDir, scope string) (HookInstall, error)
    UninstallHooks(repoDir, scope string) (HookUninstall, error)
    Events() map[Event][]string
}
```

`Event` is Worklode's vocabulary: `SessionStart`, `SessionEnd`, `Heartbeat`, `WorktreeEnter`, `PreCommit`. An unmapped event degrades that harness and never fails install. Adapters add no hook semantics; `lode-hook` behaves the same behind every name. `--harness <id>` tells `internal/hookrun` which payload shape to normalize before the guard runs; a payload missing a field Worklode needs produces a NOP.

### 5.1 `lode install`

```
lode install   [--vcs git] [--no-vcs] [--agent <id>|auto|all]... [--no-agent]
               [--scope local|project] [--skills] [--telemetry] [--no-statusline] [--json]
lode uninstall  (same flags)
```

| Flag | Rule |
|---|---|
| `--agent` | Repeatable, default `auto`: every adapter whose `Detect` returns true. `all` installs every adapter regardless, for image builds. An explicit undetected id still installs. |
| `--skills`, `--telemetry` | Opt-in, because each writes outside the hook config. |
| `--no-statusline` | Status line is on by default because its write is already conditional (§6). |
| `--agent` with `--no-agent` | Contradiction, refused. |

Coexistence is non-negotiable: never clobber, mark Worklode's entries by command identity, strip-then-write so a re-run converges. Each adapter restates that in its format: JSON entry lists (Claude Code, Codex, Copilot, Cursor), a file Worklode owns outright (the opencode shim and the Amp plugin), a package entry (Pi, §9). No adapter rewrites a config file it cannot round-trip; such a file is left untouched and reported. Managed bindings are written as `lode-hook ...` and `lode-statusline`. The install report is a list with one stanza per agent and names the events a harness could not bind.

### 5.2 Skill delivery

The skill store (09-cli-and-skills.md) is canonical at `~/.worklode/store/<hash>/`, with `~/.worklode/skills/<name>` symlinking to the current version. The store sits outside the linked skills directory so no harness that recurses lists a hash directory as a skill.

| Scope | Link | Serves |
|---|---|---|
| Personal (`--skills`, `--scope local`) | `~/.agents/skills` -> `~/.worklode/skills` | Codex, Copilot CLI, pi, opencode |
| Personal | `~/.claude/skills/<name>` -> `~/.worklode/skills/<name>`, per skill | Claude Code, which does not read `.agents/` and whose skills directory the user already populates |
| Project (inside a worktree) | `<worktree>/.agents/skills/<name>` per skill the brief lists, with `.agents/` added to `.git/info/exclude` | Any sandbox that never ran `lode install` |

`lode skill install <name> --link <harness>|all` performs the same publication standalone. In this repo `.agents/skills` symlinks to `.claude/skills`.

### 5.3 Instruction files

`lode install` writes a marker-delimited managed block into `AGENTS.md` at the repo root, stating that the repo is Worklode-tracked and that work is entered with `lode work next` (claims the top-ranked ready task and creates its worktree), with `lode task claim <id>` named second for a specific task. Content outside the markers is never touched; a missing `AGENTS.md` is created.

Claude Code reads `CLAUDE.md` and `CLAUDE.local.md`. Worklode targets `CLAUDE.local.md` (per-checkout state) and ensures that name is in the tracked `.gitignore`. A missing `CLAUDE.local.md` is created containing exactly `@AGENTS.md`; an existing one is left alone and the one-line addition is reported as a suggestion. An `AGENTS.md` symlinked to `CLAUDE.local.md` satisfies the step. An `AGENTS.md` made of `@`-imports is a hub: install resolves the imports one hop and writes the block into an imported file (one already carrying it, else an imported `CLAUDE.local.md`). Uninstall strips it from the same file.

### 5.4 Hook delivery per harness

| Worklode event | Claude Code | Codex | Copilot | Amp | opencode | pi |
|---|---|---|---|---|---|---|
| `SessionStart` | `SessionStart` | `SessionStart` | `sessionStart` | `session.start` | `session.*` | `session_start` |
| `SessionEnd` | `SessionEnd` | `SessionEnd` | `sessionEnd` | none | none | `session_shutdown` |
| `Heartbeat` | `Stop`, `StopFailure`, `SubagentStop`, `Notification` | `Stop`, `SubagentStop` | `agentStop`, `subagentStop` | `agent.start`, `agent.end` | `session.idle`, agent events | `turn_end` |
| `WorktreeEnter` | `PostToolUse:EnterWorktree` | none | none | none | none | none |
| `PreCommit` | git hook | git hook | git hook | git hook | git hook | git hook |

Claude Code, Codex, Copilot, and Cursor take a shell command in JSON bindings, so the compiled binary is the whole integration. Three harnesses take code Worklode generates from an embedded template, marked like the git hook, and deletes on uninstall: opencode gets `.opencode/plugins/worklode.ts`, Amp gets a TypeScript plugin binding `session.start` and `agent.start`/`agent.end` with tool events deliberately unbound, and pi gets the project-local package in §9. Codex hook coverage varies by release for tool, compaction, and subagent events; adapters bind those only where a missed delivery is tolerable. `PreCommit` stays a git hook and is the coverage floor for a harness with no usable session events.

Claude Code events deliberately not bound: unmatched `PostToolUse` and `PostToolBatch` (hundreds of firings for no signal), `UserPromptSubmit` (a subset of `Stop`), `WorktreeCreate`/`WorktreeRemove` (delegation hooks that would make Worklode the worktree creator), `PreCompact`/`PostCompact` (`SessionStart` re-fires with `source: compact`), `TaskCreated`/`TaskCompleted` (Claude Code's in-session todos, unrelated to Worklode tasks).

### 5.5 Telemetry

Live token accounting comes from the local Edge Agent OTLP receiver. `lode install --telemetry` configures each harness to export there: Claude Code through `CLAUDE_CODE_ENABLE_TELEMETRY`, the OTLP exporter and endpoint variables, and `OTEL_RESOURCE_ATTRIBUTES`; Codex through the `[otel]` block in `$CODEX_HOME/config.toml`, user-level and not per worktree. Edge Agent normalizes `claude_code.token.usage` metrics and Codex response log records into the usage classes Worklode accepts, deduplicates on raw provider identity, and posts complete replacement totals to `POST /api/v1/projects/{id}/session-usage`, never deltas. Worklode prices tokens from effective-dated `model_prices` rows, never from `claude_code.cost.usage`. Export failure never blocks the agent; a stopped Edge Agent may leave a gap that `lode doctor` reports.

No content telemetry: the installer never sets `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_CONTENT`, or `OTEL_LOG_RAW_API_BODIES`. A skill-usage feedback loop from `claude_code.skill_activated` is deferred: Worklode skills are unregistered, so the signal may never fire for them.

### 5.6 Degradation

| Condition | Behavior |
|---|---|
| Harness not installed on the machine | `--agent auto` skips it silently |
| Harness has no session hooks | Skills and the git heartbeat still install; the report names what was skipped |
| Event map lacks `SessionStart` | No auto-resume for that harness |
| Skill-target directory is a real directory | Per-skill links inside it; nothing Worklode did not create is deleted |
| Symlinks unavailable | Skill delivery copies the resolved store dir and reports the copy |
| Edge Agent unreachable | Exporter drops the batch; sessions and leases unaffected |
| Two harnesses on one worktree | Both bind hooks; both produce `agent_sessions` rows on the same lease |
| Harness config cannot be round-tripped | Left untouched and reported |

## 6. Status line

`lode-statusline` reads the status-line JSON on stdin and prints one line: task key and title, lease state, heartbeat freshness, and context/cost from the payload. It is a pure local read with no network call, because it re-runs on every assistant message. `lode install` binds it by default and `--no-statusline` skips it. Install claims `statusLine` only when the slot is empty or already Worklode's and reports `kept` otherwise; uninstall removes only its own binding.

The command takes no harness argument. Claude Code defined the contract (command, payload on stdin, one line on stdout) and Cursor CLI adopted it verbatim; Codex, Gemini CLI, and opencode accept no command at all, so there is nothing to dispatch on. If an incompatible payload ever appears the escape hatch is `--format <dialect>`. Pi's footer is owned by its extension (§9).

A workspace stamped with `worklode.task-id` renders the id and the branch's slug as separate words (`worklode WL-7 fix-the-thing`) in place of the branch and worktree indicators. A branch that does not carry the id contributes no slug. The read is `git config --worktree`; a repo without `extensions.worktreeConfig` falls back to the branch rendering. If the local spool for payload cost cannot be written, the line still prints without cost.

## 7. Brief, slash commands, skills, and the worker

### 7.1 `lode task brief <id> --json`

One bounded, machine-assembled payload replaces the model reading the repo to learn what a task is about. It contains: the task (id, title, `concern`, `priority`, `needs-decomposition`, status); the governing spec section, a `wl:Section` reached via `wl:governs`, never a whole document or a Plan excerpt; the `wl:affects` component set; the declared Deliverable as definition of done; the server-rendered branch and the `<worktree_dir>/<branch>` path. Injected on `SessionStart`, resume, and right after claim. If the brief is insufficient, the task needs decomposition.

### 7.2 Slash commands

| Command | Action |
|---|---|
| `/lode:next [--project P] [--strict-focus]` or `/lode:next <id>` | `claim --next` (or claim that id), create the worktree, bind the lease, inject the brief. The one way to enter Worklode mode. |
| `/lode:resume` | Re-acquire an expired lease for an existing worktree |
| `/lode:done` | Mark done (Deliverable met), release the lease, clean up per the finishing-a-branch flow |
| `/lode:block <id>` | Record the blocking dependency, mark blocked, release the lease |
| `/lode:status` | Current worktree's task, lease state, heartbeat freshness. Read-only. |
| `/lode:spec` | Graduated design authoring: only the artifacts the task warrants from {ADR, spec, task subtree}, never a Plan document |

Renewal has no slash command. Commands are thin wrappers over `lode work ... --json`; judgment lives in the skills. The `lode work` group holds `next`, `resume`, `submit`, `block`, `status`, `listen`.

### 7.3 Skills

Skills carry only what needs judgment.

| Skill | Judgment |
|---|---|
| `working-under-worklode` | When a task is actually done (Deliverable met), when to block versus push through, when to release a worktree. Explicitly excludes renewal. |
| `authoring-design-as-graph` | Output graduated to task complexity: nothing, a task subtree, or a spec/ADR. Gets crit review, writes declared-layer edges (`wl:governs`, `wl:affects`, `dct:requires`/`hasPart`), sets `needs-decomposition` when projected context would exceed the ~100k budget. |
| `architectural-review` | Reviews a design against the existing architecture read from the knowledge graph, pushing back or surfacing that the architecture must change. Resolved decisions are written back as graph edges. |

Decomposition reuses the superpowers skills (`writing-plans`, `brainstorming`, `subagent-driven-development`) and re-emits results as `lode` tasks with `concern` and `priority`. The plugin files themselves are covered in 09-cli-and-skills.md.

### 7.4 The worker

`lode-worker` is a headless subagent for unattended loops: claim, work, done or block, repeat. `lode work listen` is its CLI counterpart. It is safe because the claim is atomic, the lease is worktree-bound, and the heartbeat is commit-driven, so many workers run in parallel on a well-specified project. It is optional; the plugin works fully in interactive sessions.

## 8. Agent sessions

The backbone records which coding-agent session worked which lease, and when. Token and cost accounting per session, per project, and overhead billing (usage with no task to bill to) are described with the cost tables in 01-system-and-deployment.md.

### 8.1 Schema

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

- A child table of `leases`, because one lease outlives many sessions (restarts, `/clear`, next-day resumption).
- The unique key is per lease. A lease that expires mid-session and is re-claimed by the same live session produces a second row. `/clear` yields a new session id under the same lease. Concurrent open sessions on one lease are permitted.
- `external_session_id` is the tool's own id, namespaced by `agent`. `agent_version` is plumbed everywhere and empty for Claude Code, whose payload carries none.
- `input_tokens` and `output_tokens` are the headline rollup. Billable detail lives in `agent_session_usage`, keyed by (session, day, model, speed), priced from `model_prices`. `project_overhead_usage` and `project_daily_overhead_cost` hold usage no lease can bill.
- Cost is an amount plus an ISO 4217 code; `cost_amount IS NULL` means no cost recorded. `EndAgentSession` applies the `USD` default explicitly because a column DEFAULT fires only on INSERT.

### 8.2 Lifecycle

- A row is created on the first heartbeat from a session, under the lease active at that moment.
- A heartbeat on a closed row re-opens it (`ended_at` back to NULL). The row spans first touch to last without recording gaps.
- `last_seen_at` is bumped on every heartbeat. Running sessions are `ended_at IS NULL AND last_seen_at > now() - interval '30 minutes'`; the window is a read-side constant.
- `ended_at` is stamped by the session-end hook and by `closeLease` / `CloseActiveLease` for any session still open on the lease being closed. A close by that route emits no `agent_session.ended` event because the lease's own `lease.released` / `lease.expired` event records the transition, so started/ended pairs are deliberately unbalanced.

### 8.3 Store and API

| Store function | Behavior |
|---|---|
| `TouchAgentSession(ctx, taskID, actorID, agent, version, sessionID, usage)` | Start or heartbeat. Wrapped in `RecordEvent("cli", "agent-session-<leaseID>-<agent>-<sessionID>", "agent_session.started", ...)` whose apply is `INSERT ... ON CONFLICT DO NOTHING`; first-seen is idempotent through the events table's `(source, external_id)` uniqueness. The `last_seen_at` bump and `ended_at` clear run as a plain UPDATE outside the event, in a transaction that first takes `SELECT ... FOR SHARE` on the lease and skips the write when the lease is released. The insert takes the same lock. Optional `usage` is written after the touch, replace-not-accumulate. |
| `EndAgentSession(ctx, taskID, actorID, agent, sessionID, usage)` | Records `agent_session.ended` with a random event id, sets `ended_at`, writes supplied tokens and cost. Idempotency comes from the `ended_at IS NULL` predicate: a repeat close matches no rows, fails apply, and rolls the event back. |
| `ReportProjectSessionUsage` | Replacement usage totals for one session across a project, including rows whose lease is gone. No holder check. No separate overhead-only entry point exists. |

Both lease-bound functions resolve the active lease and require the caller to be its holder, returning `ErrNotFound` for a non-holder so the two failure cases stay indistinguishable. The lease lock is required because a plain uncorrelated `EXISTS` is evaluated once against the statement snapshot, and the foreign key does not serialize the insert against `UPDATE leases SET released_at` (`FOR KEY SHARE` versus `FOR NO KEY UPDATE`). All four paths that touch both tables lock `leases` before `agent_sessions`.

| Endpoint | Body |
|---|---|
| `POST /api/v1/tasks/{id}/agent-session` | `{agent, agent_version, session_id, usage?}` |
| `POST /api/v1/tasks/{id}/agent-session/end` | `{agent, session_id, input_tokens?, output_tokens?, cost_amount?, cost_currency?, usage?}` |
| `POST /api/v1/projects/{id}/session-usage` | replacement session totals from Edge Agent |

`cost_amount` crosses the wire as a decimal string and is validated in Go against the column shape. The surface is named `agent-session`, never `session`, which `internal/api/session.go` owns for web and CLI auth. `ProjectCost` reports the combined task-attributed and overhead total with overhead broken out.

### 8.4 Hook wiring

| `lode-hook` event | Call | Session id from | Claude Code binding |
|---|---|---|---|
| `session-start` | `TouchAgentSession` after `ensureLease` | payload | `SessionStart` |
| `heartbeat` | `TouchAgentSession` | payload | `Stop`, `StopFailure`, `SubagentStop`, `Notification` |
| `worktree-enter` | `TouchAgentSession` on the entered worktree's lease | payload | `PostToolUse` matcher `EnterWorktree` |
| `worktree-exit` | `EndAgentSession` for the exited lease's row | payload | none |
| `pre-commit` | `TouchAgentSession` alongside `RenewLease` | marker file | git `pre-commit` |
| `session-end` | `EndAgentSession` before removing the marker | payload | `SessionEnd` |

- `Stop` is the backbone of liveness, one firing per turn. `StopFailure` fires instead of `Stop` on an API error, `SubagentStop` covers a fan-out turn longer than the staleness window, and `Notification` (`permission_prompt`, `idle_prompt`, `agent_needs_input`) covers a session blocked on a human.
- `heartbeat`, `session-end`, and `worktree-enter` require only that the hook runs inside a git worktree, so a main-checkout session reports too; tokens that resolve to no task, or to a task this actor no longer holds, bill as project overhead.
- One session can work several tasks through `EnterWorktree` / `ExitWorktree`: entering opens a row under the new lease with the same `external_session_id`, exiting stamps `ended_at` on the row it leaves. Both move the `worklode-session.json` marker, which `heartbeatDue` reads.
- `worktree-exit` has no Claude Code binding: `ExitWorktree`'s tool input carries no path and cwd has already been restored to the returned-to directory when `PostToolUse` fires, so a cwd fallback would close the wrong row. It requires an explicit path in `tool_input` and is a NOP without one. The row it would have closed drops out of the running window and is closed when the lease closes.
- Heartbeats are debounced client-side: `worklode-session.json` records `last_heartbeat_at`, and a heartbeat within 60s makes no backbone call. `worktree-enter` / `worktree-exit` are not debounced.
- `SessionStart` re-fires on `startup|resume|clear|compact|fork`; `TouchAgentSession` is idempotent. `SessionEnd` fires on `clear` and `resume` as well as exit, and that is correct: one session ends, another begins.
- A git `pre-commit` has no stdin and reads the session id from the marker. A missing or stale marker means no heartbeat, not an error. The marker's pid is that of the exited hook process, so `sessionMarkerFresh` is effectively always false; the backbone's `last_seen_at` is the liveness signal.
- `internal/transcript` parses Claude Code transcripts for historical import only; live usage comes from Edge Agent (§5.5).

### 8.5 Error handling

An unreachable backbone degrades to a stderr warning and the session proceeds. A heartbeat for a task the caller no longer holds returns `ErrNotFound` and is warned, never retried. Repeat heartbeats are idempotent by construction.

## 9. Pi agent integration

Pi is integrated through a native, distributable extension rather than a shell-hook configuration. The adapter owns no task, lease, worktree, or authentication state; `lode` remains the sole interface and Pi owns its sign-in and project-trust decision.

The package lives at `plugins/pi/lode/` (`package.json` with a `pi` manifest, `extensions/worklode.ts`, `skills/`, `README.md`), imports only Pi's declared peer packages, and is valid both as a local package and as a future npm package. `lode install --agent pi` merges the relative source `./plugins/pi/lode` into the repo root's `.pi/settings.json`, preserving every other entry and recognizing its own resolved path on rerun. `lode uninstall --agent pi` removes only that entry. Pi loads project-local packages only after the user trusts the project; the installer neither pre-approves trust nor invokes login. If Pi is unavailable, an explicit install reports a prerequisite and auto-detection skips it.

`extensions/worklode.ts` registers event handlers and commands, runs `lode` with `ctx.cwd` under a short timeout and Pi's cancellation signal, and turns failures into UI notifications. Every call is best effort and never blocks Pi from starting, ending a turn, or shutting down. A non-Worklode directory and an idle task are normal states that clear the status. The extension owns one identity, `lode`, for all UI registrations and clears its status on shutdown.

| Pi event | Worklode action |
|---|---|
| `session_start` | `lode-hook session-start` |
| `session_shutdown` | `lode-hook session-end` (also fires for reload, new-session, resume, fork, clone) |
| `turn_end` | `lode-hook heartbeat` (chosen over `agent_end` because a run may auto-retry) |
| `agent_settled` | refresh status only |

User-only commands `/lode:next [task]`, `/lode:resume [task]`, `/lode:done`, `/lode:block [reason]`, `/lode:status` delegate to the corresponding `lode work` commands and refresh status. No model-callable tool is exposed. Pi-native guidance under `plugins/pi/lode/skills/` duplicates the operational prose of the Claude tree without Claude frontmatter. After session start, every command, and `agent_settled`, the extension runs `lode work status --json` and renders task key, title, lease, and heartbeat health with `ctx.ui.setStatus("lode", value)`, guarded by `ctx.hasUI`. Status refreshes may contact the backbone and so never sit on streaming events.

Acceptance: install and uninstall preserve foreign settings and converge; a trusted project loads the package from the matching checkout; one Pi session in a worktree yields session-start, heartbeat, and session-end facts without a model instruction; Pi stays usable if any lifecycle or status call fails. Go tests cover the settings merge (foreign package, duplicate local path, invalid JSON, file emptied by uninstall); TypeScript tests cover argument construction, failure rendering, event mapping, status parsing, UI guards, and status clearing.

## 10. Reconciliation and setup diagnosis

Worklode learns about GitHub through webhooks. When one never arrives, the backbone keeps a stale picture. Three commands cover the gap, split on a permission boundary.

| Command | Audience | Auth | Job |
|---|---|---|---|
| `lode doctor` | developer | none, works offline | is my setup correct |
| `lode project health [repo]` | operator | admin | is ingestion working for this repo |
| `lode task reconcile [--repo X \| --task Y] [--since D] [--dry-run]` | operator | admin | repair what ingestion missed |

Reconcile compares what the backbone recorded against what GitHub did. `lode graph drift` (07-knowledge-graph-and-search.md) compares declared architecture against observed code; the two share nothing.

### 10.1 `lode doctor`

Client-side, useful with the server unreachable. Checks in order, each reporting pass/fail and the fix:

1. Config file found, and where the `.worklode`/`.lode` walk-up located it.
2. `server` set and reachable.
3. Token present (OS keychain or `LODE_TOKEN`) and accepted, via `GET /api/v1/whoami`.
4. `current_project` set and the project exists.
5. Git hooks installed in this repo.
6. Inside a worktree: it maps to a task, and that task holds a live lease.
7. Edge Agent reachable, so no token-accounting gap.

Exits non-zero on any failure, so it can run from a hook or CI step.

### 10.2 `lode project health [repo]`

Per mapped repo, from the server: App installed (`GET /repos/{owner}/{name}/installation`, reported installed / not installed / unchecked, where only GitHub's own 404 counts as not installed; checks run concurrently under one deadline), last webhook received and event types seen, unapplied events (`*.ignored` and nil-apply rows awaiting replay), and unmapped senders (repos sending events that map to no project). No argument reports every repo. A repo that never received a webhook, or whose last delivery predates its mapping, is the signal to run reconcile.

### 10.3 `lode task reconcile`

Two engines behind one command, cheapest first. Facts are repaired, findings reported, `--dry-run` suppresses writes for both. `--since` accepts RFC 3339 or a Go duration, resolved against the server clock. Leaving all filters off is the scheduled-caller case.

Engine 1, replay stored events. Events whose apply never completed: `*.ignored` (repo unmapped at delivery) and failed applies. The webhook path records the event row in one transaction and applies in a second (`store.RecordEventThenApply`), so a failed apply answers 500 but leaves the row with `applied_at` NULL. Apply routing is a transport-independent `Apply(tx, st, source, eventID, type, payload)` shared by the webhook handler and the replayer. A replayed apply carries the original event's id so `state_log` points at the real GitHub event. `events.applied_at timestamptz` is set when an apply completes by either path; replay is order-safe because every fact upsert checks the fact's own last-modified time and `Transition` checks the from-state. One run reads a fixed-size oldest-first batch and reports `truncated` when full; the error list is capped the same way. A redelivered event whose row exists unapplied gets its apply re-run; only an applied event is a no-op duplicate.

Engine 2, poll GitHub. Candidates: tasks not `done`/`abandoned`, plus tasks landed but below their repo's `done_state`. Per task, mint an installation token and read the PRs' real `state`/`merged`/`merge_commit_sha`, whether recorded commits are on the default branch, and which releases contain them. Missing facts are written through `UpsertPR` / `InsertTaskCommit` / `AppendMainCommit`, then `ResolveDelivery` runs; it derives delivery state from facts, so there is no state machine to replay. One `source='system'` event per run (type `reconcile.poll`, `external_id` = run id) receives attribution for facts and transitions. Requests batch per repo against one installation token.

Output is one report per run, per engine, with `--json` for scheduled callers.

### 10.4 API

| Endpoint | Gate | Returns |
|---|---|---|
| `POST /api/v1/reconcile` | admin | run report; body `{repo?, task?, since?, dry_run}`; synchronous |
| `GET /api/v1/repos/doctor[?repo=]` | admin | ingestion-health report |
| `GET /api/v1/whoami` | auth only | calling actor's id, kind, admin flag |

Acceptance: a replayed `*.ignored` event produces exactly the typed-table and `state_log` result of a live delivery with the transition referencing the original event, and a second replay changes nothing; a task whose PR merged while ingestion was down reaches its delivery state in one run, attributed to `reconcile.poll`, and a second run is a no-op; both doctors name the fix for each failure; every command emits deterministic `--json`.

## 11. Inbox import

The inbox is otherwise fed only by live webhooks, so everything before a repo was mapped would stay invisible. `lode inbox import` fetches a repo's issues and PRs server-side and upserts them as inventory.

| Decision | Choice |
|---|---|
| Where it runs | Server-side, which holds the App installation token |
| Request shape | Synchronous, page-capped, no job table |
| Write path | `store.UpsertIssue` / `store.UpsertPR`, unchanged |
| Lifecycle replay | Never. Import writes rows and drives no `Transition`, `CloseActiveLease`, `InsertTaskCommit`, or `ResolveDelivery`; replaying history would resolve delivery for tasks never in flight and roll it into parents. |
| Default selection | `--state open`, issues only |
| Authorization | admin, like `lode project repo add` |
| Idempotency | `(repo, number)` upsert; promoted rows untouched |
| Linking an issue to a task | `lode inbox link`, the third triage verb |
| `triage_state` for a link | `promoted` |
| Event-subscription mismatch | warn on `project repo add`, never gate |

Correlation still happens inside `UpsertPR` (head ref, then body) and no-ops when the task does not exist.

### 11.1 Fetch

`internal/githubauth/list.go`:

```go
func (a *AppAuth) ListIssues(ctx context.Context, repo, state string, since time.Time, maxPages int) ([]Issue, bool, error)
func (a *AppAuth) ListPulls(ctx context.Context, repo, state string, maxPages int) ([]PullRequest, bool, error)
```

Both page `?sort=updated&direction=asc&per_page=100&page=N` until a short page or `maxPages` (20), returning `truncated`. Ascending update order puts the truncated tail at the newest end, so `--since` is a resume cursor for issues (`/issues` accepts `since`). `/pulls` has no `since`, so PRs filter on `updated_at` client-side and a truncated PR list cannot be resumed; the CLI tells the caller to narrow with `--state open`. `issues.truncated` and `prs.truncated` are reported independently and `newest_updated_at` comes from the issues stream only. `ListIssues` skips every entry with a `pull_request` key. The structs carry exactly the fields the webhook appliers read; the API layer assembles `store.Issue` / `store.PullRequest`.

### 11.2 API and CLI

`POST /api/v1/inbox/import`, admin:

```json
{"repo": "owner/name", "state": "open", "include_prs": false,
 "since": "2026-01-01T00:00:00Z", "dry_run": false}
```

Preconditions in order: 503 when no App is configured, 422 on an unknown `state`, 404 when `ProjectForRepo` finds no mapping. GitHub round trips run outside any transaction under a 60s bound; the result is applied in one `RecordEvent("cli", extID, "inbox.imported", payload, apply)`. `apply` first reads the existing `(repo, number)` set so the response distinguishes new from updated:

```json
{"repo": "owner/name", "issues": {"new": 38, "updated": 3},
 "prs": {"new": 0, "updated": 0}, "truncated": false, "dry_run": false}
```

`--dry-run` fetches and counts, returns the same shape with `dry_run: true`, and records no event. CLI: `lode inbox import <repo> [--state open|closed|all] [--include-prs] [--since <date>] [--dry-run]`. The CLI prints the exact rerun command for truncated issues.

### 11.3 Staging and linking

`lode inbox promote` takes `--draft` (task lands in state `draft`, published with `lode task publish`) and `--parent <id>` (a `child_of` edge added in the same `RecordEvent`, with a named 404 pre-check on the parent). `needs_decomposition` and `lode task decompose` are the other staging lever.

`lode inbox link <repo> <number> <task-id>` records that an issue is already covered by a task. `store.LinkIssue` requires `triage_state = 'new'` (else `ErrBadTransition`) and an existing task (else `ErrNotFound`), then sets `triage_state = 'promoted'` and `task_id`. `POST /api/v1/inbox/link`, event type `issue.linked`.

### 11.4 Event-subscription check

`AppAuth.SubscribedEvents(ctx)` reads `events` from `GET /app` under the App JWT. `hooks.HandledEvents()` exports the event names `applyFunc` routes, and `applyFunc` switches over that same list so the two cannot drift. `lode project repo add` compares them and returns missing events in a `warnings` field, non-gating: the mapping is already committed and a slow GitHub must not hold the response.

Bulk dismiss after `--state all` on a mature repo is a recorded follow-up. Importing documents, components, and GitHub projects into the graph is deferred to the document model in 05-documents.md; `AppAuth.Tarball` is the fetch mechanism when that lands.

## Sources

WL-SPEC-8 (plugin and harness integration, with folded amendments from 041, 051, 053, 061, 063), WL-SPEC-12 (agent sessions, with 052 and 063), WL-SPEC-41 (Pi integration), WL-SPEC-13 (reconciliation and setup diagnosis, with 061), WL-SPEC-20 (inbox import).

## Open questions

- Who removes the worktree after `/lode:done`: the plugin or the human's finishing-a-branch flow.
- How a hook detects an already-running session in this worktree without a model call; the backbone's `last_seen_at` (§8.2) is the candidate signal.
- Whether `lode doctor` should verify each adapter's config assumptions against the installed harness version, given the capability tables change monthly.
- Whether the skill-usage signal can work at all for unregistered Worklode skills, or needs a `PostToolUse` hook on reads under the skill store path.
- Whether engine 2's unscoped candidate set is small enough to run without `--since` at real task counts, and whether reconcile should become a scheduled server loop.
- Whether `worktree.branch` is recoverable from the status-line payload for hook-created worktrees, or only from cwd.
