# Worklode Plugin (spec 008) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `docs/specs/008-worklode-plugin.md`: the worktree-bound pickup lifecycle (`lode next`/`resume`/`done`/`block`/`status`), `lode task brief`, compiled `lode hook` handlers with `--next` daisy-chaining, `lode install-git-hooks`, and the `lode` Claude Code plugin (slash-command skills, `working-under-worklode` skill, `lode-worker` agent, hooks.json).

**Architecture:** Two repos. Go machinery lands in **worklode** (`~/git/sunstone/worklode`): new `internal/worktree` package (worktree naming/parse/identity), `lode hook <event>` subcommands (the compiled hook binary *is* the CLI), lifecycle commands, and a `brief` endpoint + lease-rebind endpoint server-side. The Claude Code plugin lands in **claude-plugins** (`~/git/sunstone/claude-plugins`) as `plugins/lode/` — thin skills invoking `lode … --json`, hooks.json wiring editor events to `lode hook …` via a guard script. Hooks are NOPs outside a `wt/<id>-<slug>` worktree.

**Tech Stack:** Go (worklode repo), Claude Code plugin format (skills/agents/hooks.json). Requires plans 004 and 005 merged.

**Verified Claude Code facts this plan relies on (do not re-research):**
- Hook events: `SessionStart`, `SessionEnd`, `PreToolUse`, `WorktreeCreate`, `WorktreeRemove` exist. **`EnterWorktree`/`ExitWorktree` do NOT exist** — the spec's EnterWorktree auto-resume maps to `SessionStart` (fires in the session's cwd) + `WorktreeCreate`; ExitWorktree release maps to `WorktreeRemove` + `SessionEnd`.
- `PreToolUse` matchers filter tool *name* only; filtering on `git commit` uses `"matcher": "Bash"` plus `"if": "Bash(git commit *)"` (permission-rule syntax).
- Hooks receive JSON on stdin (`cwd`, `session_id`, `hook_event_name`, `tool_input` for PreToolUse); a `SessionStart` hook injects context by printing `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"…"}}` and exiting 0.
- Plugin layout: `plugins/lode/.claude-plugin/plugin.json` (manifest only), `skills/<name>/SKILL.md` (invoked as `/lode:<name>`), `agents/*.md`, `hooks/hooks.json` with `${CLAUDE_PLUGIN_ROOT}` available in hook commands.
- Skill dynamic context: `` !`command` `` runs before the model sees the skill; `$ARGUMENTS`/`$1` substitution; `disable-model-invocation: true` for user-only commands.

**Settled decisions:**
- Worktree directory: `<repo-root>/wt/<WL-n>-<slug>`; branch: `wl/<WL-n>-<slug>` (keeps existing PR correlation). Worktree lease identity string: `<hostname>:<abs-worktree-path>` (plan 004 helper).
- Claim-then-bind: `claim --next` can't know the worktree path pre-claim, so `lode next` claims with a temporary identity `<hostname>:<repo-root>#pending-<8hex>`, creates the worktree, then rebinds via a new endpoint. Failure between claim and rebind → release.
- Session liveness (spec Q008.3): marker file `worklode-session.json` (`{"session_id","pid","started_at"}`) in the worktree's private git dir (`git rev-parse --git-dir` → `.git/worktrees/<name>/`); stale when the pid is dead. No backbone change.
- `SessionEnd` does **not** release the lease (the lease lives with the worktree across sessions — spec §Hold); it only removes the session marker. Release fires on `WorktreeRemove` and on explicit `done`/`block`. This resolves the spec's hook-table/Hold tension in favor of Hold.
- `/lode-*` slash commands become **`/lode:next`, `/lode:resume`, `/lode:done`, `/lode:block`, `/lode:status`** (plugin namespacing is `/plugin:skill`; a `lode-next` skill would render as `/lode:lode-next`).
- `lode done` does not remove the worktree (spec Q008.1): it marks done + releases + prints the cleanup command; `WorktreeRemove` auto-release covers whoever deletes it.
- **Deferred, out of scope here:** `/lode:spec`, `authoring-design-as-graph`, `architectural-review` skills (all require the spec-006 knowledge graph), and the Task↔GitHub-Issue mirror (Q008.4). Brief fields `governing_design` and `affected_components` are emitted as `null` with the shape reserved.
- The 24/7 `lode-worker` agent ships as a minimal agent definition (optional in v1 per spec).

---

## Part A — worklode repo (Go)

### Task A1: `internal/worktree` package

**Files:**
- Create: `internal/worktree/worktree.go`, `internal/worktree/worktree_test.go`

**Steps:**

- [x] **Step 1: Write tests first** for the pure functions:

```go
func TestDirName(t *testing.T)      // DirName("WL-7", "fix-the-thing") == "wt/WL-7-fix-the-thing"
func TestParseDir(t *testing.T)     // ParseDir("/repo/wt/WL-7-fix-the-thing") -> ("WL-7", true);
                                    // ParseDir("/repo") -> ("", false); ParseDir("/repo/wt/nope") -> ("", false)
func TestBranchName(t *testing.T)   // BranchName("WL-7","fix-the-thing") == "wl/WL-7-fix-the-thing"
```

- [x] **Step 2: Implement:**

```go
// Package worktree maps Worklode task identity onto git worktrees: the
// deterministic wt/<id>-<slug> directory name, its wl/<id>-<slug> branch,
// and the lease identity string the backbone stores.
package worktree

var dirRe = regexp.MustCompile(`^(WL-\d+)(?:-[a-z0-9-]+)?$`)

func DirName(taskID, slug string) string   { return "wt/" + taskID + "-" + slug }
func BranchName(taskID, slug string) string { return "wl/" + taskID + "-" + slug }

// ParseDir returns the task id when path's last two segments are
// wt/<WL-n>-<slug>. This is the uniform hook guard: ok=false ⇒ NOP.
func ParseDir(path string) (taskID string, ok bool)

// Identity returns "<hostname>:<abs path>" — the lease worktree identity.
func Identity(path string) (string, error)

// Root walks up from dir to the enclosing git worktree root
// (git -C dir rev-parse --show-toplevel); ok=false outside a repo.
func Root(dir string) (string, bool)

// GitDir returns the worktree-private git dir (rev-parse --git-dir, abs).
func GitDir(root string) (string, error)
```

Move/reuse `WorktreeIdentity` from plan 004 (`internal/cli/worktree.go`) into this package; update its callers.
- [x] **Step 3:** `go test ./internal/worktree/` green. Commit.

### Task A2: Server — `GET /tasks/{id}/brief` + lease rebind

**Files:**
- Modify: `internal/store/tasks.go` or new `internal/store/brief.go` (+ test)
- Modify: `internal/store/leases.go` (+ test)
- Modify: `internal/api/server.go`, `internal/api/lifecycle.go` (+ tests)

**Steps:**

- [x] **Step 1: Store `Brief`:**

```go
type Brief struct {
	Task              Task     // id, title, concern, priority, needs_decomposition, state, project
	Body              string
	Branch            string   // wl/<id>-<slug>
	OpenBlockers      []Task   // blocks edges still open (id+title+state)
	Lease             *Lease   // active lease or nil
	GoverningDesign   *string  // reserved: spec 006 (null in v1)
	AffectedComponents []string // reserved: spec 006 (nil in v1)
	DefinitionOfDone  *string  // reserved: Deliverable, spec 006 (null in v1)
}

func (s *Store) Brief(ctx context.Context, taskID string) (*Brief, error)
```

One bounded payload — task row, branch from `SlugifyTitle`, open blockers via one query, active lease. No file contents, no unbounded lists.
- [x] **Step 2: Store `RebindLeaseWorktree(ctx, taskID, actorID, worktree string) error`** — updates `worktree` on the active lease held by `actorID` (non-holder → `ErrNotFound`, same probe-resistance as Renew), recorded as event `lease.rebound`; 23505 on `leases_active_worktree` → `ErrLeased`.
- [x] **Step 3: API.** `GET /api/v1/tasks/{id}/brief` (bearer) → the Brief as JSON (snake_case keys: `task`, `body`, `branch`, `open_blockers`, `lease`, `governing_design`, `affected_components`, `definition_of_done`). `POST /api/v1/tasks/{id}/lease/worktree` `{"worktree": "..."}` → rebind.
- [x] **Step 4: Tests** at store and handler level (brief for task with blockers + lease; rebind by holder ok, by non-holder 404). Green, commit.

### Task A3: CLI lifecycle — `lode next`, `resume`, `done`, `block`, `status`, `task brief`

**Files:**
- Create: `internal/cmd/lifecycle.go` (+ `lifecycle_test.go`)
- Modify: `internal/cmd/task.go` (add `brief` subcommand), `internal/cli/client.go` (Brief, ClaimNext already from plan 005, Rebind)

**Steps:**

- [x] **Step 1: `lode next [<id>] [--project P] [--strict-focus] [--json]`** — the one way to enter Worklode mode:
  1. Resolve repo root (`worktree.Root(".")`; error if absent or if cwd is already inside a `wt/` worktree).
  2. Claim: with `<id>` → `POST /tasks/{id}/claim`; without → claim-next. Worktree field: `<hostname>:<root>#pending-<8hex>`.
  3. None ready → print `{"claimed":false,...}` / "no ready task", exit 0.
  4. `git worktree add <root>/wt/<id>-<slug> -b wl/<id>-<slug>` (branch may exist from an earlier attempt: then `git worktree add <dir> wl/<id>-<slug>`).
  5. Rebind lease to `worktree.Identity(dir)`.
  6. Fetch + print the brief (`--json`: `{"claimed":true,"worktree":"<abs dir>","branch":"…","brief":{…}}`).
  On any failure after claim: release the lease, remove a half-created worktree, exit non-zero.
- [x] **Step 2: `lode resume [<dir>] [--json]`** — re-acquire an existing worktree: resolve dir (default cwd) → `ParseDir` → task id. If active lease exists for this worktree identity → `renew`. If none (sweeper reclaimed; task back in `ready`) → `claim <id>` with this worktree's identity. Print brief. Outside a `wt/` worktree → error.
- [x] **Step 3: `lode done [--json]`** — inside a worktree: task id from `ParseDir` → `POST /tasks/{id}/done` → release → print confirmation + `git worktree remove` cleanup hint. **`lode block --on <blocker-id> [--json]`** — adds `blocks` edge (`blocker blocks current`) via existing edges API, releases the lease, prints confirmation. Both error politely outside a worktree.
- [x] **Step 4: `lode status [--json]`** — read-only: worktree dir, task id, brief.Task summary, lease state (held/expired/none, expires_at, renewed_at freshness), session-marker state. Never mutates.
- [x] **Step 5: `lode task brief <id> [--json]`** — plain fetch/print of A2's endpoint.
- [x] **Step 6: Tests:** httptest-server CLI tests for each command's happy path + guard errors; `lode next` end-to-end against a real temp git repo + ephemeral store via the e2e pattern (worktree actually created, lease rebound to its path, cleanup on forced rebind failure). Green, commit.

### Task A4: `lode hook <event>` + daisy-chain

**Files:**
- Create: `internal/cmd/hook.go`, `internal/hookrun/hookrun.go`, `internal/hookrun/hookrun_test.go` (logic lives in `hookrun` so it's testable; `cmd` stays thin)

**Steps:**

- [x] **Step 1: Contract.** `lode hook <event> [--next <cmd> [arg...]]` where `<event>` ∈ `session-start | session-end | pre-commit | worktree-create | worktree-remove`. Common behavior:
  - Read the hook payload from stdin (tolerate empty/non-JSON: git pre-commit has none). Determine the working dir: payload `cwd`, else `$PWD`.
  - **Uniform guard:** `worktree.Root(dir)` → `worktree.ParseDir(root)`; no match ⇒ do nothing. With `--next` ⇒ `syscall.Exec` the next command (stdin already consumed — pass the raw payload via env `LODE_HOOK_PAYLOAD` and re-feed via a pipe is NOT needed for git hooks; for exec, write payload to the child's stdin by re-execing through `/bin/sh -c` is overkill — instead spawn with `os/exec`, feed payload to child stdin, exit with child's code; use plain `exec.Command`, not execve, and document why: stdin replay). Without `--next` ⇒ exit 0.
  - All backbone calls have a short timeout (2s) and **never fail the editor event**: on error, print a `systemMessage` warning JSON (Claude Code hooks) or a stderr warning (git), exit 0.
- [x] **Step 2: Events.**
  - `session-start`: guard → if inside a Worklode worktree: ensure lease (active → renew if it expires within 30m; expired/none → re-acquire via the `resume` logic); write the session marker; fetch brief; emit `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"<compact brief text>"}}`. If *outside*: cheap offer scan — list `<root>/wt/*` dirs, for each `ParseDir` hit query lease state (one `GET /tasks/{id}/brief` each, max 5); any with expired/no lease and a stale/absent session marker → additionalContext one-liner: "Worklode worktree wt/… (WL-n: title) is abandoned — `/lode:resume wt/…` to adopt it." No claim, no model call.
  - `session-end`: guard → remove session marker. Nothing else.
  - `pre-commit`: guard → `renew` (the commit-cadence heartbeat). Expired-and-swept (task reclaimed) → warning on stderr, still exit 0 (never block a commit).
  - `worktree-create`: guard on the *created* path (payload) → if it's a `wt/` dir with an expired lease and no live session → re-acquire (auto-resume; provably ours). Else NOP.
  - `worktree-remove`: guard on the removed path → `release` (canonical "done holding this").
- [x] **Step 3: Tests** (`hookrun` unit + subprocess-style):
  - Guard NOP: every event, run against a plain temp git repo → no API calls (assert with an httptest server that fails the test on any request), exit 0.
  - `--next` chain: guard-NOP with `--next touch <tmpfile>` → file exists, exit 0 (downstream ran).
  - `pre-commit` inside a fixture worktree renews (httptest asserts the renew call).
  - `session-start` inside a worktree emits valid `additionalContext` JSON.
  Green, commit.

### Task A5: `lode install-git-hooks`

**Files:**
- Create: `internal/cmd/installhooks.go` (+ test `installhooks_test.go`)

**Steps:**

- [x] **Step 1: Behavior.** In the current repo's shared hooks dir (`git rev-parse --git-path hooks`, honoring `core.hooksPath`):
  - Compose the chain target: existing `pre-commit` file (if present and not ours) → rename to `pre-commit.pre-lode`, chain to it. Else if `.pre-commit-config.yaml` exists at repo root → chain to `pre-commit` (the framework binary). Else no chain.
  - Write `pre-commit` (0755):

```sh
#!/bin/sh
# worklode-hook v1 — installed by `lode install-git-hooks`; do not edit.
exec lode hook pre-commit --next <target with args, or nothing> "$@"
```

  (No `--next` clause when there's nothing to chain.)
  - **Idempotent:** a file containing the `# worklode-hook` marker is rewritten in place (re-point, never chain to ourselves, never double-rename the preserved hook).
- [x] **Step 2: Tests** in temp git repos: fresh install; re-run unchanged (idempotent); existing third-party pre-commit preserved as `.pre-lode` and chained; `.pre-commit-config.yaml` present → chains the framework; installed hook + guard NOP = `git commit` succeeds in a plain repo without a lode server. Green, commit.

### Task A6: worklode repo finishing

**Files:**
- Modify: `.gitignore` (add `wt/`), `README.md` (plugin section: install `lode`, `lode install-git-hooks`, the `/lode:*` flow, pointer to the claude-plugins plugin)

**Steps:**

- [x] **Step 1:** `.gitignore` + README. Full `go build ./... && go vet ./... && go test ./...` green. Commit.

## Part B — claude-plugins repo (`~/git/sunstone/claude-plugins`)

Work on a branch; this repo reviews via PR. **First look at one existing plugin (e.g. `plugins/sunstone-dev/`) and mirror its manifest/marketplace conventions exactly** (including however plugins are registered in the repo's marketplace file).

### Task B1: Plugin skeleton + hooks

**Files:**
- Create: `plugins/lode/.claude-plugin/plugin.json`:

```json
{
  "name": "lode",
  "description": "Worklode: worktree-bound task pickup for agents (claim/resume/done/block via the lode CLI)",
  "version": "0.1.0",
  "hooks": "./hooks/hooks.json"
}
```

- Create: `plugins/lode/hooks/hooks.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/hook.sh session-start", "timeout": 10}]}
    ],
    "SessionEnd": [
      {"matcher": "", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/hook.sh session-end", "timeout": 10}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "if": "Bash(git commit *)", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/hook.sh pre-commit", "timeout": 10}]}
    ],
    "WorktreeCreate": [
      {"matcher": "", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/hook.sh worktree-create", "timeout": 10}]}
    ],
    "WorktreeRemove": [
      {"matcher": "", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/scripts/hook.sh worktree-remove", "timeout": 10}]}
    ]
  }
}
```

- Create: `plugins/lode/scripts/hook.sh` (0755):

```sh
#!/bin/sh
# Worklode hook shim: NOP unless the lode CLI is installed. All guard logic
# lives in `lode hook` (compiled, fast); this only bridges the plugin event.
command -v lode >/dev/null 2>&1 || exit 0
exec lode hook "$1"
```

**Steps:**

- [x] **Step 1:** Create the three files; register the plugin wherever the repo's marketplace listing lives (mirror an existing entry).
- [x] **Step 2: Manual verification note** (put in the PR description): with the plugin installed and `lode` on PATH, a session in a plain repo shows no Worklode output; in a `wt/…` worktree, SessionStart injects the brief. Commit.

### Task B2: Slash-command skills

Six thin skills, all `disable-model-invocation: true` (user-invoked commands), each delegating judgment to `working-under-worklode` and mechanics to `lode … --json`.

**Files:**
- Create: `plugins/lode/skills/next/SKILL.md`:

```markdown
---
name: next
description: Claim the next ready Worklode task (or a specific one), create its worktree, and start working in it
argument-hint: "[task-id] [--project P] [--strict-focus]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

## Claim result
!`lode next $ARGUMENTS --json`

If `claimed` is false: tell the user nothing is ready and stop.
Otherwise a worktree was created and the lease is bound to it. cd into the
`worktree` path from the JSON, read the `brief`, and start the task. The brief
is the context contract — do NOT spelunk the repo to reconstruct context; if
the brief is insufficient, say so: the task likely needs decomposition.
Load the working-under-worklode skill before starting.
```

- Create: `plugins/lode/skills/resume/SKILL.md` — same shape around `` !`lode resume $ARGUMENTS --json` `` ("re-acquire this worktree's task; report what state it was in; continue from the brief").
- Create: `plugins/lode/skills/done/SKILL.md`:

```markdown
---
name: done
description: Mark the current Worklode task done and release its lease
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Before running anything, verify the Deliverable is actually met (see
working-under-worklode: done means the definition-of-done holds, not "code
written"). If it is not met, say what's missing and stop.

Then run `lode done --json`, report the result, and surface the printed
worktree-cleanup instruction to the user.
```

- Create: `plugins/lode/skills/block/SKILL.md` — verify a real blocker exists (working-under-worklode judgment), then `lode block --on <id> --json`; if the blocker task doesn't exist yet, create it first with `lode task add` and use its id.
- Create: `plugins/lode/skills/status/SKILL.md` — `` !`lode status --json` `` , read-only report of task/lease/heartbeat; never mutates.

**Steps:**

- [x] **Step 1:** Write the five skills (`/lode:spec` is deferred — see plan header).
- [x] **Step 2:** Sanity-check each against the plugin skill format (frontmatter fields verified in this plan's header). Commit.

### Task B3: `working-under-worklode` skill + `lode-worker` agent

**Files:**
- Create: `plugins/lode/skills/working-under-worklode/SKILL.md` — judgment only (spec §Skills). Content contract:

```markdown
---
name: working-under-worklode
description: Use when working inside a Worklode worktree (wt/<id>-<slug>) — the done/block/release judgment loop for leased tasks
---

# Working under Worklode

You are in a worktree bound to a leased task. Machinery (hooks) already
handles lease renewal, resume, and release — NEVER think about heartbeats,
renewal, or lease TTLs; committing at a normal cadence is the heartbeat.

## The three judgments that are yours

**Done** — a task is done when its definition-of-done / Deliverable holds,
not when code is written. Check the brief's definition_of_done (when null,
the task body is the contract). Tests pass, the deliverable exists where it
should. Then /lode:done.

**Block** — block (don't push through) when progress requires a decision or
artifact outside this task's scope: a missing dependency, a design decision
someone must make, an unmet precondition. Record it honestly with
/lode:block so the frontier reflects reality. Push through minor obstacles
that are within scope.

**Release without done/block** — if you must abandon the worktree with the
task genuinely still workable (wrong fit, user redirected you), just stop;
removing the worktree releases the lease, and an untouched worktree ages out
to the sweeper. Don't mark done what isn't.

## Context discipline

The brief is the context contract. If it is not enough to do the work, that
is a signal the task needs decomposition — set it with
`lode task edit <id> --needs-decomposition=true` and /lode:block or report,
rather than spelunking the repo to reverse-engineer intent.
```

- Create: `plugins/lode/agents/lode-worker.md`:

```markdown
---
name: lode-worker
description: Headless Worklode worker — claims the next ready task, works it to done or blocked, repeats. For unattended loops on well-spec'd projects.
tools: *
skills: working-under-worklode
---

You are an unattended Worklode worker. Loop:

1. `lode next --json`. If `claimed` is false, stop and report "no ready work".
2. cd into the worktree; follow the brief and the working-under-worklode
   skill. Commit as you go (commits are the lease heartbeat).
3. Finish with `lode done --json` (Deliverable met) or
   `lode block --on <id> --json` (real blocker), then return to 1.

Never claim more than one task at a time; never work outside the task's
worktree; never mark done what does not meet its definition of done.

## Model selection when you delegate

If a task is large enough that you dispatch subagents (decomposition,
subagent-driven implementation, review), you become a coordinator — pick the
tier per the work, and **always set `model` explicitly on every dispatch.**
Omitting it does NOT inherit your model; it silently falls back to the
top-level session model, running mechanical work on the most expensive tier.

- Fully-specified implementation task (exact files/code/tests, no open
  design decisions) → `model: "sonnet"`.
- Spec review / code review, and any task with unknowns (debugging, design
  gaps, plan-vs-reality conflicts) → `model: "opus"`.
- If a Sonnet implementer hits ambiguity, escalate that task to Opus rather
  than letting it improvise.

Doing the leased task's work yourself (no subagents) needs no dispatch — this
applies only when you fan out.
```

**Steps:**

- [x] **Step 1:** Write both files as above (adjust `tools:` syntax to match existing agents in the repo). Keep the "Model selection when you delegate" section verbatim — it mirrors the repo-root MODEL_SELECTION.md and is the operational copy the autonomous worker reads.
- [x] **Step 2:** Commit; open the claude-plugins PR (plugin + marketplace entry, B1–B3 together).

### Task B4: Acceptance walkthrough (manual, scripted in the PR)

**Steps:**

- [x] **Step 1:** Script the spec-008 acceptance list against a local server (`docker compose up`) and record results in the worklode PR description:
  1. `/lode:next` → `wt/<id>-<slug>` worktree + bound lease + injected brief; plain-checkout session untouched (criteria 1).
  2. `git worktree remove` → lease released; `git commit` in the worktree renews; neither fires outside (criteria 2, via `lode status` before/after).
  3. Sweeper-expired lease re-acquired by `/lode:resume` and by auto-resume, no new claim (criteria 3; shrink TTL via claim `ttl` to test).
  4. `lode install-git-hooks` chaining + idempotency (criteria 4 — also covered by Go tests in A5).
  5. `lode task brief` bounded payload (criteria 5 — Go tests in A2).
  6. Daisy-chain `--next` (criteria 6 — Go tests in A4).
  7. Skills judgment-only, no renewal logic (criteria 7 — `working-under-worklode` explicitly excludes heartbeats; graph-fed skills deferred to spec 006, noted in PR).

---

## Acceptance criteria mapping (spec 008)

All seven criteria → Task B4's walkthrough, backed by: 1→A3/B1/B2, 2→A4, 3→A3/A4, 4→A5, 5→A2, 6→A4, 7→B3 (partial: two graph-fed skills deferred until spec 006 exists).
