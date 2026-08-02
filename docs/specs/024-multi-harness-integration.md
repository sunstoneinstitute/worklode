---
status: draft
issued: 2026-08-01
requires:
  - 008-worklode-plugin.md
  - 012-agent-sessions.md
  - 016-org-wide-skills.md
  - 022-prometheus-metrics.md
---
# Spec 024 — Multi-harness agent integration

## Purpose & scope

Worklode's agent integration is Claude-Code-shaped. `lode install --agent claude-code` is the
only accepted value, and it writes hook bindings into `.claude/settings*.json`. Everything else
in the design is already harness-neutral: spec 016 refuses to register skills in any native
registry precisely so that "any agent that can read a file participates", and spec 012's
`agent_sessions.agent` CHECK already lists `codex`, `cursor`, `aider`, `opencode`, `pi`, `amp`.

The gap is delivery. Every harness now reads the *same* `SKILL.md`, fires the *same* lifecycle
moments, and differs only in **which directory it looks in** and **which config file names the
hook**. That is a table, not a design problem. This spec makes the harness a **pluggable
adapter** — one capability table, one installer, one skill store, N harnesses — and adds the two
Claude Code surfaces Worklode does not yet use: the **status line** and **OpenTelemetry**.

**Out of scope (consumed, not redefined):** the lease lifecycle, `lode task brief`, and the
guard invariant (008); the skill registry, recommendation and content-addressed store (016);
the `agent_sessions` schema and transcript pricing (012). A harness adapter decides *which files
to write* and *which events map to `lode hook <event>`*. It never introduces a second
coordination model.

**v1:** the adapter registry, `--agent` detection, skill delivery via `.agents/skills`, hook
installation for the four harnesses with a native shell-hook mechanism, and the Claude Code
status line. **v2:** OTLP ingest, the TypeScript plugin shims for opencode/pi, and the skill
usage feedback loop that 016 §v2 defers.

---

## Findings — what the harnesses expose (August 2026)

Verified against primary sources: the Claude Code docs, the `openai/codex` and
`earendil-works/pi` sources, and the GitHub Docs content repo. Cells marked *(unverified)* come
from secondary sources only and must be confirmed before an adapter ships against them.

### The one thing that changed: `SKILL.md` became a standard

`SKILL.md` — YAML frontmatter with `name` + `description`, a Markdown body, sibling files
alongside — is now an open standard (agentskills.io), and a **shared discovery convention has
emerged**: `~/.agents/skills/` for personal skills and `.agents/skills/` in the repo for project
skills. Codex resolves it (`AGENTS_DIR_NAME = ".agents"` in `codex-rs/core-skills/src/loader.rs`,
walking `cwd` and ancestors to the repo root), Copilot CLI documents it, pi documents it, and
opencode reads it. **Claude Code does not** — it reads `~/.claude/skills/` and `.claude/skills/`
only.

That is the single highest-leverage fact in this spec: **one symlink of the Worklode skill store
into `~/.agents/skills/` serves four harnesses at once**, and Claude Code needs one more.

### Table 1 — Skill and instruction delivery

| | Project skill dirs | Personal skill dirs | Reads `.agents/` | Symlinks followed | Instruction file |
|---|---|---|---|---|---|
| **Claude Code** | `.claude/skills/<n>/SKILL.md` (nested dirs load lazily) | `~/.claude/skills/` | **No** | **Yes**, documented and deduped by target | `CLAUDE.md`, `.claude/rules/*.md`; `@AGENTS.md` import documented |
| **Codex CLI** | `.agents/skills/` (cwd + ancestors), `.codex/skills/` | `~/.agents/skills/`, `~/.codex/skills/` (deprecated, still read) | **Yes** | **Yes** for User/Repo/Admin scopes; ignored for bundled system skills | `AGENTS.md` |
| **Copilot CLI** | `.github/skills/`, `.claude/skills/`, `.agents/skills/` | `~/.copilot/skills/`, `~/.agents/skills/` | **Yes** | *(unverified)* | `AGENTS.md`, `.github/copilot-instructions.md`, `.github/instructions/**/*.instructions.md`; `@`-includes supported |
| **pi** | `.pi/skills/`, `.agents/skills/` (cwd + ancestors) | `~/.pi/agent/skills/`, `~/.agents/skills/` | **Yes** | *(unverified)* | `AGENTS.md` |
| **opencode** | `.opencode/skills/`, `.claude/skills/`, `.agents/skills/` | `~/.config/opencode/skills/`, `~/.claude/skills/`, `~/.agents/skills/` | **Yes** | *(unverified)* | `AGENTS.md` (wins over `CLAUDE.md` when both exist) |
| **Amp** | `.amp/skills/`, `.agents/skills/` *(unverified)* | `~/.amp/skills/` *(unverified)* | *(unverified)* | *(unverified)* | `AGENTS.md`, falls back to `CLAUDE.md` |
| **Cursor CLI** | `.cursor/skills/` *(unverified)* | `~/.cursor/skills/` *(unverified)* | *(unverified)* | *(unverified)* | `AGENTS.md`, `.cursor/rules/*.mdc` |
| **Gemini CLI** | extension `skills/<n>/SKILL.md` | *(unverified)* | *(unverified)* | *(unverified)* | `GEMINI.md` |

Two adapter-relevant notes:

- **pi tolerates a name/directory mismatch on purpose** ("that rule is suboptimal for shared
  skill directories used across multiple agent harnesses"). Worklode's store is exactly such a
  directory, so nothing needs renaming per harness.
- **pi and Codex both accept extra roots from config** — pi via a `skills` array in
  `settings.json`, Codex via its layer stack. Pointing a harness at `~/.worklode/skills` directly
  is an alternative to symlinking, harness by harness.

### Table 2 — Lifecycle hooks

| | Mechanism | Config location | Events Worklode cares about | Can block? |
|---|---|---|---|---|
| **Claude Code** | JSON bindings; handler types `command`, `prompt`, `agent`, `http`, `mcp_tool`; sync or background | `~/.claude/settings.json`, `.claude/settings.json`, `.claude/settings.local.json`, plugin `hooks/hooks.json`, skill/agent frontmatter | ~30 events. Currently used: `SessionStart`, `SessionEnd`, `Stop`, `StopFailure`, `SubagentStop`, `Notification`, `PostToolUse:EnterWorktree`. **Unused and relevant:** `CwdChanged`, `FileChanged`, `InstructionsLoaded`, `PreCompact`/`PostCompact`, `TaskCreated`/`TaskCompleted`, `UserPromptSubmit`, `Setup` | Yes (`PreToolUse`, `UserPromptSubmit`, `PermissionRequest`) |
| **Codex CLI** | `hooks.json` layered through the config stack; `/hooks` TUI toggles them; legacy `notify` still fires on turn completion | `~/.codex/hooks.json` and project/managed layers; `allow_managed_hooks_only` in `requirements.toml` | 11 events: `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop` | Yes (`PreToolUse`, `PermissionRequest`) |
| **Copilot CLI** | JSON files, one `bash` and one `powershell` key per handler, plus `cwd`, `env`, `timeoutSec` | `.github/hooks/*.json` (repo), `~/.copilot/hooks/*.json` (personal) | `sessionStart`, `sessionEnd`, `userPromptSubmitted`, `preToolUse`, `postToolUse`, `agentStop`, `subagentStop`, `errorOccurred` | Yes (`preToolUse` approves/denies) |
| **Gemini CLI** | `hooks/hooks.json` inside an extension directory; documented migration path from Claude Code hooks | extension dir (not the `gemini-extension.json` manifest) | *(event list unverified)* | *(unverified)* |
| **Amp** | `amp.hooks` array of `{event, action}` in the settings JSON | `~/.config/amp/settings.json`, or `$AMP_SETTINGS_FILE` | `tool:pre-execute`, `tool:post-execute`; actions include `send-user-message`, `redact-tool-input` | Yes, by interrupting with a user message |
| **Cursor CLI** | `.cursor/hooks.json` *(unverified)* | project `.cursor/` | *(unverified)* | *(unverified)* |
| **opencode** | **TypeScript plugin**, not shell hooks; dozens of events (`session.idle`, `tool.execute.before`, `file.edited`, …) | `.opencode/plugins/`, `~/.config/opencode/plugins/`, or npm packages listed in `opencode.json` | Session + tool + file events | Yes (mutates tool args in `tool.execute.before`) |
| **pi** | **TypeScript extension**, not shell hooks; `pi.on(event, handler)` over session/agent/model/tool/input event families | `~/.pi/agent/extensions/`, `.pi/extensions/`, or `packages`/`extensions` entries in `settings.json` | `session_start`, `session_shutdown`, `tool_call`, model/agent events | Yes (block or modify tool calls) |

The split is clean: **six harnesses take a shell command**, so the existing compiled
`lode hook <event>` binary is the whole integration. **Two take TypeScript**, so they need a
~30-line shim that shells out to the same binary — still no second coordination model.

### Table 3 — Observability and UI surfaces

| | Status line | OpenTelemetry | Session id reachable from a hook | Transcript on disk |
|---|---|---|---|---|
| **Claude Code** | **Yes** — `statusLine.command` gets a rich JSON payload on stdin: `session_id`, `prompt_id`, `cost.total_cost_usd`, `cost.total_lines_added/removed`, `context_window.*`, `rate_limits.five_hour/seven_day`, `model.id`, `workspace.git_worktree`, `pr.number`, `worktree.*`. `refreshInterval` and `padding` supported | **Yes** — metrics (`claude_code.session.count`, `cost.usage`, `token.usage`, `lines_of_code.count`, `commit.count`, `pull_request.count`, `active_time.total`), events (incl. **`claude_code.skill_activated`**), traces (beta). Configured entirely by env vars | Yes (`session_id` on every payload) | Yes (`transcript_path` on every payload) |
| **Codex CLI** | *(not found)* | **Yes** — `otlp-grpc` / `otlp-http` exporters for traces and metrics, with headers and mTLS | Yes | Yes (rollout files under `$CODEX_HOME`) |
| **Gemini CLI** | *(unverified)* | **Yes** — OTLP, configured through `.gemini/settings.json` | *(unverified)* | *(unverified)* |
| **Copilot CLI** | *(not found)* | *(not found)* | Yes (hook payload) | *(unverified)* |
| **pi** | **Yes** — `ctx.ui.setStatus(id, text)` / `setFooter` from an extension | *(not found)* | Yes | Yes (session tree files) |
| **opencode** | *(unverified)* | *(not found)* | Yes | Yes |
| **Amp** | *(not found)* | *(not found)* | Yes (thread id) | Threads sync to ampcode.com |

### What this buys Worklode

Three tiers, in descending order of value per unit of work:

1. **Skill delivery becomes near-free.** `~/.agents/skills → ~/.worklode/skills` is one symlink
   covering Codex, Copilot, pi and opencode. Claude Code needs a second one. The
   content-addressed store, lazy fetch, and brief integration from 016 are unchanged.
2. **The heartbeat becomes portable.** Six harnesses accept a shell command on session
   start/end and turn completion — the exact events `internal/hookrun` already handles. Spec
   008's Q008.2 ("auto-resume is Claude-Code-only, degraded coverage accepted for v1") stops
   being a permanent concession for most of the fleet.
3. **The 016 v2 usage signal already exists, emitted by the harness.**
   `claude_code.skill_activated` carries `skill.name`, `invocation_trigger`, `skill.source` and
   `session.id`, and `session.id` is the join key `agent_sessions` already stores. Which skills
   get used on which tasks — the ranking signal 016 defers to v2 — is an ingest endpoint away,
   with no hook and no model call.

---

## Design

### The harness adapter

A new `internal/harness` package holds one adapter per harness plus a registry keyed by id
(`claude-code`, `codex`, `copilot`, `cursor`, `gemini`, `amp`, `opencode`, `pi`). An adapter is
data plus a small amount of file-writing behaviour, not a strategy hierarchy:

```go
// Harness describes one coding agent's integration surface. Everything an
// adapter can express is a location or a name; the behaviour on the other end
// of those names is `lode hook`, unchanged across harnesses.
type Harness interface {
    ID() string

    // Detect reports whether this harness is configured for repoDir or the
    // user, so `--agent auto` installs only what is actually in use.
    Detect(repoDir string) (bool, error)

    // SkillTargets lists the directories this harness reads skills from, in
    // the scope requested. Install links the Worklode store into them.
    SkillTargets(repoDir, scope string) ([]string, error)

    // InstallHooks binds the events in Events() to `lode hook <event>` in this
    // harness's own config file, preserving every foreign entry.
    InstallHooks(repoDir, scope string) (HookInstall, error)
    UninstallHooks(repoDir, scope string) (HookUninstall, error)

    // Events maps Worklode's lifecycle events to this harness's event names.
    // An unmapped Worklode event degrades that harness, it never fails install.
    Events() map[Event][]string
}
```

`Event` is Worklode's vocabulary — `SessionStart`, `SessionEnd`, `Heartbeat`, `WorktreeEnter`,
`PreCommit` — and each adapter maps it onto the harness's names. Claude Code's existing
`claudeBindings` table in `internal/cmd/claude.go` becomes the `claude-code` adapter's `Events()`
verbatim; the file's settings-merge machinery (`stripLodeHooks`, `appendBinding`,
`isLodeHookEntry`) moves into the adapter unchanged. **This spec adds no new hook semantics.**

### `lode install` grows a harness dimension

```
lode install   [--vcs git] [--no-vcs] [--agent <id>|auto|all]... [--no-agent]
               [--scope local|project] [--skills] [--telemetry] [--statusline] [--json]
lode uninstall  (same flags)
```

- `--agent` becomes **repeatable**, and defaults to `auto`: every adapter whose `Detect` returns
  true is installed. A repo with Claude Code and Codex configured gets both from one command.
- `--agent all` installs every adapter regardless of detection, for image builds and sandbox
  provisioning where no harness is configured yet at install time.
- An explicit `--agent <id>` naming an undetected harness still installs — asking for it *is* the
  detection signal.
- `--skills`, `--telemetry` and `--statusline` opt into the surfaces below. They are flags rather
  than defaults because each writes outside the hook config, and `lode install` has always been
  conservative about files it did not create.

The existing contradiction rules (`--agent` with `--no-agent`, both integrations skipped) and
the existing report shape carry over; the report grows from one agent stanza to a list.

**Coexistence is non-negotiable and already solved.** The rule from 008 — never clobber, mark
our entries by the `lode hook ` command prefix, strip-then-write so a re-run converges rather
than duplicates — is a *format-independent* rule. Each adapter restates it in its own format:
JSON entry lists (Claude Code, Codex, Copilot, Cursor), a settings array (Amp), a file we own
outright (the TypeScript shims). No adapter is allowed to rewrite a config file it cannot
round-trip.

### Skill delivery — one store, many doorways

The 016 store is unchanged: `~/.worklode/skills/.store/<hash>/` is canonical and immutable,
`~/.worklode/skills/<name>` symlinks to the most recent version. What changes is that Worklode
now also **publishes** that directory into the places harnesses look.

**Personal scope** (`lode install --skills`, default `--scope local`):

| Link | Serves |
|---|---|
| `~/.agents/skills` → `~/.worklode/skills` | Codex, Copilot CLI, pi, opencode |
| `~/.claude/skills/<name>` → `~/.worklode/skills/<name>` | Claude Code |

The first is one symlink for four harnesses. The second is per-skill rather than
directory-level, because `~/.claude/skills` is a directory users already own and populate;
replacing it with a symlink to the Worklode store would strip their own skills. Claude Code
documents that it follows a `<skill-name>` symlink and loads a skill once even when reachable
from two locations, so per-skill links are the supported shape.

`~/.worklode/skills/.store/` sits inside the linked directory and would be walked by any harness
that recurses. Every harness in Table 1 discovers skills by finding `SKILL.md`, so a store dir
would surface each version as a duplicate skill. **The store therefore moves down one level to
`~/.worklode/store/`, leaving `~/.worklode/skills/` containing name symlinks only** — a
one-migration change to `skillstore.Root`, invisible to the brief (which carries the resolved
`.store` path today and would carry the resolved `store` path after).

**Project scope** (inside a Worklode worktree): the brief's lazy fetch (016) additionally links
`<worktree>/.agents/skills/<name>` for each skill the brief lists, and adds `.agents/` to
`.git/info/exclude` rather than `.gitignore` — the links are machine-local and must not become a
commit. Project scope is what makes a sandbox work: a container that never ran `lode install`
still gets exactly the skills its task named, in a directory five harnesses read.

`lode skills install <name>` gains `--link <harness>|all` for the same publication step
standalone.

### Hook delivery

Six harnesses take a shell command, so the compiled binary is the integration and the adapter is
a table. Two do not:

- **opencode and pi get a shim.** `lode install --agent opencode` writes a single TypeScript file
  into `.opencode/plugins/worklode.ts` (or `~/.pi/agent/extensions/worklode.ts`) that subscribes
  to the harness's session and tool events and spawns `lode hook <event>` with the same JSON on
  stdin the shell harnesses send. The shim is generated from an embedded template, carries the
  same marker comment the git hook does, and is the one file in this design Worklode owns
  outright — so uninstall deletes it rather than editing around foreign content.
- **Payload shape differs per harness.** `lode hook` learns `--harness <id>`, defaulting to
  `claude-code` for compatibility with every binding already installed. `internal/hookrun`
  normalizes the payload to its existing internal shape before the guard runs. The guard
  invariant is untouched: *no bound worktree ⇒ no Worklode behaviour*, and a harness whose
  payload omits a field Worklode needs simply produces a NOP, never an error surfaced to the user.

The event map, per harness, for the events 008 defines:

| Worklode event | Claude Code | Codex | Copilot | Amp | opencode / pi (shim) |
|---|---|---|---|---|---|
| `SessionStart` | `SessionStart` | `SessionStart` | `sessionStart` | — | `session.*` / `session_start` |
| `SessionEnd` | `SessionEnd` | `SessionEnd` | `sessionEnd` | — | `session_shutdown` |
| `Heartbeat` | `Stop`, `StopFailure`, `SubagentStop`, `Notification` | `Stop`, `SubagentStop` | `agentStop`, `subagentStop` | `tool:post-execute` | `session.idle` / agent events |
| `WorktreeEnter` | `PostToolUse:EnterWorktree` | — | — | — | — |
| `PreCommit` | (git hook) | (git hook) | (git hook) | (git hook) | (git hook) |

`PreCommit` stays where it is — the git hook is editor-agnostic and already covers every harness,
including the ones with no session hooks at all. **A harness with no usable event mapping still
gets the git heartbeat**, which is the coverage floor 008 already accepts.

### Status line (Claude Code first)

`lode statusline` reads Claude Code's JSON on stdin and prints one line: the current task key and
title, lease state, heartbeat freshness, and context/cost from the payload itself. Installed by
`lode install --statusline`, which sets `statusLine.command` only when the key is absent —
a status line is a personal choice and Worklode must not silently replace one.

**The hot-path constraint is the whole design.** The status line re-runs on every assistant
message, so it may not make a server call. `lode statusline` reads the worktree's local lease
marker and prints from that; it is a pure local read with no network. The payload's
`session_id`, `cost.total_cost_usd` and token counts are appended to a per-session spool file,
and the **existing heartbeat hook flushes the spool to the backbone** on its own cadence. Cost
and token facts thus reach `agent_sessions` continuously, from the harness's own accounting,
without a second network path and without touching the transcript pricing in 012 — which stays
authoritative, with the spool as a live approximation between flushes.

### Telemetry ingest (v2)

`lode serve` gains an OTLP/HTTP receiver at `/api/v1/otlp/v1/logs` and
`/api/v1/otlp/v1/metrics`, authenticated by the same `wl_` bearer token as the rest of the API.
`lode install --telemetry` writes the env block that points the harness at it — for Claude Code:
`CLAUDE_CODE_ENABLE_TELEMETRY`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`,
`OTEL_EXPORTER_OTLP_PROTOCOL`, the endpoint, and `OTEL_EXPORTER_OTLP_HEADERS` carrying the token.

What Worklode consumes, and nothing more:

| Signal | Consumed as |
|---|---|
| `claude_code.skill_activated` (`skill.name`, `invocation_trigger`, `skill.source`, `session.id`) | The **016 §v2 usage feedback loop**: which sessions actually read which skills, joined to tasks through `agent_sessions.external_session_id` |
| `claude_code.cost.usage`, `claude_code.token.usage` | A cross-check on the transcript-derived cost in 012 — never a replacement, since 012 prices from effective-dated `model_prices` rows and the harness prices client-side |
| `claude_code.session.count`, `claude_code.commit.count`, `claude_code.pull_request.count` | Fleet-level counters alongside the spec 022 domain metrics |

**Privacy is a default, not an option.** Worklode's installer never sets `OTEL_LOG_USER_PROMPTS`,
`OTEL_LOG_ASSISTANT_RESPONSES`, `OTEL_LOG_TOOL_CONTENT`, or `OTEL_LOG_RAW_API_BODIES`. Note the
consequence for skill telemetry: without `OTEL_LOG_TOOL_DETAILS=1`, `skill.name` arrives as the
placeholder `"custom_skill"` for user and third-party plugin skills. Worklode-distributed skills
are read from a path rather than registered (016 §Purpose), so they are unlikely to appear at
all — which is why the usage loop is **v2 and gated on Q024.4** below, not assumed to work.

Codex and Gemini CLI export OTLP to the same endpoint with different attribute names; a
per-source mapper at the ingest boundary keeps one storage shape. No harness-specific table.

### Instruction files

`lode install` writes a **marker-delimited managed block** into `AGENTS.md` at the repo root —
the file six of the eight harnesses read — containing the two facts an agent needs before a
brief exists: that this repo is Worklode-tracked, and that work is entered through
`lode task claim`. Content outside the markers is never touched, and a repo with no `AGENTS.md`
gets one created.

Claude Code reads `CLAUDE.md`, not `AGENTS.md`. Where no `CLAUDE.md` exists, `lode install`
creates one containing exactly `@AGENTS.md` — the pattern Claude Code's own memory documentation
prescribes. Where one exists, Worklode leaves it alone and reports the one-line addition as a
suggestion; a `CLAUDE.md` is authored prose and Worklode has no business editing it.

---

## Degradation

| Condition | Behavior |
|---|---|
| Harness not installed on the machine | `--agent auto` skips it silently. Nothing is written for a harness that isn't there. |
| Harness has no session hooks (Cursor, Gemini until verified) | Skills and the git `PreCommit` heartbeat still install; the report names what was skipped and why. |
| Harness event map lacks `SessionStart` | No auto-resume for that harness — exactly the coverage 008 Q008.2 already accepts, now stated per harness instead of globally. |
| A skill-target directory exists as a real directory, not a symlink | Worklode links per-skill inside it rather than replacing it, and never deletes a path it did not create. |
| Symlinks unavailable (Windows without Developer Mode) | Skill delivery falls back to copying the resolved store dir, and `lode skills install` reports the copy so a stale copy is diagnosable. |
| OTLP endpoint unreachable | The harness's exporter drops the batch; sessions and leases are unaffected. Telemetry is never on a coordination path. |
| Status line spool cannot be written | `lode statusline` still prints; the cost line is simply absent. It never fails the harness's render. |
| Two harnesses on one worktree | Both bind hooks; both produce `agent_sessions` rows against the same lease. 012 explicitly permits concurrent open sessions on one lease. |

## Dependencies

- **008 (plugin)** — the event vocabulary, the `--next` daisy-chain contract, and the coexistence
  rule this spec generalizes. `lode hook` gains `--harness`; nothing else changes.
- **012 (agent sessions)** — `agent_sessions.agent` already accepts the harness ids; the status
  line spool and OTLP ingest both write through it.
- **016 (org skills)** — the store, the brief's lazy fetch, and the v2 usage-signal placeholder
  this spec proposes to fill. The store root moves one level down (`~/.worklode/store/`).
- **022 (Prometheus metrics)** — the OTLP receiver's own health counters ride the existing
  `/metrics` surface rather than inventing a second one.
- **External** — the harness config formats in Tables 1–3, each of which is a moving target; see
  Q024.1.

## Open questions

- **Q024.1 — Verification cadence for the capability tables.** These formats change monthly.
  Does `lode doctor` gain a check that each adapter's assumptions still hold on the installed
  version (config file present, expected keys parse), or is drift caught only when an install
  fails? A doctor check is cheap and this spec's tables have a short half-life.
- **Q024.2 — Store relocation migration.** Moving `.store` out of `~/.worklode/skills/` orphans
  existing installs. Does `lode install` migrate silently, or does `lode doctor` report and
  `lode skills install` re-fetch? Re-fetch is content-addressed and therefore safe, just slower.
- **Q024.3 — Adapter ownership.** Eight adapters is eight surfaces to keep current. Should the
  four with fully verified sources (Claude Code, Codex, Copilot, pi) be supported, and the rest
  ship as community-contributable tables — a data file rather than Go code?
- **Q024.4 — Whether skill telemetry can work at all.** `claude_code.skill_activated` fires for
  skills the harness *registers*. Worklode skills are deliberately unregistered (016). The
  signal may therefore never fire for them, in which case the 016 v2 loop needs its own
  mechanism — most likely a `PostToolUse` hook matching reads under the skill store path. Resolve
  before building OTLP ingest for this purpose.
- **Q024.5 — Status line and worktree binding.** The payload carries `workspace.git_worktree` and
  a `worktree.*` block, but `worktree.branch` is documented as absent for hook-based worktrees —
  which is what Worklode creates. Confirm `wt/<id>-<slug>` is recoverable from `cwd` alone before
  the status line depends on anything else.

## Acceptance criteria

1. `lode install` in a repo with Claude Code and Codex configured installs both, writes each
   harness's own config format, and preserves every pre-existing entry in both files. Re-running
   converges rather than duplicates, and `lode uninstall` restores both to their prior state.
2. `lode install --skills` makes an org skill readable by Codex, Copilot, pi and opencode through
   `~/.agents/skills`, and by Claude Code through `~/.claude/skills/<name>`, from a single store
   copy. No harness lists a `.store`/`store` hash directory as a skill.
3. A `SessionStart` in a `wt/<id>-<slug>` worktree re-acquires an expired lease under **each**
   harness that maps the event — same behaviour, same backbone rows, different payload on stdin.
4. A session under a harness with no session hooks still renews its lease on `git commit`, and
   `lode install --json` names the events that harness could not bind.
5. `lode statusline` prints task, lease state and heartbeat freshness with **no** network call,
   and the cost the harness reported reaches `agent_sessions` on the next heartbeat flush.
6. A harness config file that Worklode cannot round-trip is left untouched and reported, never
   partially rewritten.
7. `lode install --telemetry` sets no content-logging variable, and an unreachable OTLP endpoint
   changes nothing about claiming, briefing, or releasing a task.

## Sources

Primary, consulted 2026-08-01:

- Claude Code — [hooks](https://docs.claude.com/en/docs/claude-code/hooks),
  [status line](https://docs.claude.com/en/docs/claude-code/statusline),
  [monitoring & OTEL](https://docs.claude.com/en/docs/claude-code/monitoring-usage),
  [skills](https://docs.claude.com/en/docs/claude-code/skills),
  [memory](https://docs.claude.com/en/docs/claude-code/memory)
- Codex CLI — `codex-rs/hooks/src/lib.rs` (event list), `codex-rs/core-skills/src/loader.rs`
  (skill roots, `.agents`, symlink policy), `codex-rs/otel/src/config.rs` (exporters), and
  `docs/config.md` (managed hooks), in [openai/codex](https://github.com/openai/codex)
- Copilot CLI — GitHub Docs content:
  [add skills](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills),
  [hooks](https://docs.github.com/en/copilot/concepts/agents/hooks),
  [custom instructions](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions)
- pi — `packages/coding-agent/docs/skills.md` and `extensions.md` in
  [earendil-works/pi](https://github.com/earendil-works/pi)
- Agent Skills standard — [agentskills.io](https://agentskills.io/specification)

Secondary (cells marked *unverified* above): [opencode docs](https://opencode.ai/docs/skills/),
[Amp Owner's Manual](https://ampcode.com/manual), [Gemini CLI
docs](https://geminicli.com/docs/extensions/), Cursor community documentation.
