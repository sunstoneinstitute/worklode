---
status: accepted
implements:
  - docs/specs/024-multi-harness-integration.md#sec-3.1
  - docs/specs/024-multi-harness-integration.md#sec-3.2
  - docs/specs/024-multi-harness-integration.md#sec-3.4
  - docs/specs/024-multi-harness-integration.md#sec-3.7
---
# Multi-harness 1/3: adapter core and installer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 1 of 3. Task numbers restart at 1 in each plan file;
cross-part dependencies are the `requires:` frontmatter edges between the
files, never task numbers. This plan holds 8 tasks;
`2026-08-03-multi-harness-2-skill-delivery.md` holds 4;
`2026-08-03-multi-harness-3-statusline.md` holds 5. Part 1 must be merged
before parts 2 and 3 start; parts 2 and 3 are independent of each other.

- **Part 1 — adapter core and installer (8 tasks):** the `internal/harness`
  package with a registry and four adapters (claude-code, codex, copilot,
  amp), `lode install`/`uninstall` growing a repeatable `--agent` with
  `auto`/`all`, `lode hook --harness` payload normalization, the
  agent-CHECK-widening migration, and the `AGENTS.md` managed block.
  *Checkpoint:* `lode install` in a repo with two harnesses configured
  installs both and re-runs converge (spec 024 acceptance 1).
- **Part 2 — skill delivery (4 tasks):** the store-root move
  (`~/.worklode/store/`), publication into `~/.agents/skills` and
  `~/.claude/skills/<name>`, `--skills`/`--link`, and project-scope worktree
  links. *Checkpoint:* acceptance 2.
- **Part 3 — status line and cost spool (5 tasks):** the lease marker,
  `lode statusline`, the per-session cost spool, the heartbeat flush into
  `agent_sessions`, and `--statusline`. *Checkpoint:* acceptance 5.

**Goal:** Turn the Claude-Code-only agent integration into a pluggable
adapter: one `internal/harness` registry, four v1 adapters, and an installer
that detects and installs every harness actually in use — while `lode hook`
stays the single coordination path for all of them.

**Architecture:** `internal/cmd/claude.go`'s settings machinery
(`claudeBindings`, `stripLodeHooks`, `appendBinding`, `isLodeHookEntry`,
read/write helpers) moves verbatim into `internal/harness` as the
`claude-code` adapter; codex, copilot and amp adapters restate the same
strip-by-`lode hook `-prefix coexistence rule in their own config formats.
`lode install` resolves `--agent` (repeatable; `auto` = every adapter whose
`Detect` fires, `all` = every adapter) into a list and reports per agent,
including the Worklode events an adapter could not bind. `lode hook` learns
`--harness <id>` (default `claude-code`), which `internal/hookrun` uses to
normalize the stdin payload before the existing guard — no new hook
semantics, no handler changes. A migration widens the `agent_sessions.agent`
CHECK, which is missing `copilot`.

**Tech Stack:** Go 1.26, cobra, stdlib `testing` (table-driven, `t.Fatalf`),
golang-migrate. No new dependencies.

**Spec:** `docs/specs/024-multi-harness-integration.md`

---

## What exists vs. what this builds

- `lode install`/`uninstall`: `internal/cmd/install.go` — `--agent` is a
  single string validated against the one constant `agentClaudeCode`
  (`install.go:17,62`), `hookTargets{vcs, agent string}` (`install.go:22`),
  results `installResult{VCS, Agent}` with one `agentInstall` stanza
  (`install.go:73-105`). Tests: `internal/cmd/install_test.go` (flag
  resolution via `targetsFor`, `install_test.go:17`).
- Claude Code settings writer: `internal/cmd/claude.go` — everything in the
  file moves. Tests: `internal/cmd/claude_test.go` (helpers `readSettings`,
  `commandsFor`).
- `lode hook`: `internal/cmd/hook.go` (`parseHookArgs`, flag parsing
  disabled) → `internal/hookrun/hookrun.go` (`Payload` at `hookrun.go:84`,
  `agentName` at `hookrun.go:228` — `LODE_AGENT` env, default `claude-code`).
- Git pre-commit hook: `internal/cmd/githooks.go` — untouched; it is already
  every harness's coverage floor (spec 024 §3.4).
- `agent_sessions.agent` CHECK:
  `deploy/base/migrations/0004_agent_sessions.up.sql` lists
  `claude-code, codex, cursor, aider, opencode, pi, amp, other` — **no
  `copilot`** (contradicting spec 024 §5's claim that 012 already accepts
  the harness ids). Task 3 fixes the schema; the spec-012 amendment is
  handled separately, not here.
- `ns/` carries no harness enum (checked: no `wlc:` concept lists agents), so
  the CHECK widening needs no ontology mirror.

**Migration number:** provisional. `0009` is the highest on main and `0010`
is claimed by `2026-08-03-spec-shorthand-references.md`, so this plan uses
`0011` and expects `./scripts/check-migrations.sh` to renumber at execution
time.

**Plan-level decisions (assumptions under the spec's open questions, all
deliberate):**

1. **v1 adapters are exactly the four native-shell-hook harnesses of §3.4's
   event map: claude-code, codex, copilot, amp.** cursor's config format is
   unverified (§2, Q024.3), gemini-cli has been discontinued and gets no
   adapter at all, and opencode/pi need the v2 TypeScript shim; naming any
   of them in `--agent` errors with the supported list. The registry design
   leaves adding one as one file each.
2. **Codex, copilot and amp hook configs are written at user level** (their
   documented stable layer). The repo-local/user-global distinction is a
   Claude Code concept; `lode hook`'s guard (NOP outside `wt/<id>-<slug>`)
   is what makes a user-global binding repo-safe, so no per-repo layer is
   needed. `--scope` keeps selecting the Claude Code settings file only.
3. **The codex/copilot/amp config shapes below are the spec's §2.3 findings,
   verified 2026-08-01.** Each adapter task starts with a re-verification
   step against the primary source named in spec 024 §8; a drifted format is
   adjusted in the adapter table, and a format that cannot express a command
   handler downgrades that adapter to skills + git heartbeat (spec §4 row 2),
   reported as unbound events — never a broken write.
4. **No `lode doctor` check** (Q024.1 open): drift is caught by the
   per-adapter verification steps at build time and by install failing
   loudly, nothing more in v1.
5. **The `AGENTS.md` managed block (§3.7) is in scope** despite not being in
   §1's v1 list: it is part of `lode install`'s surface, unmarked as v2, and
   no other plan will ever cover it.

**Out of scope (v2 or open-question, per spec):** OTLP ingest and
`--telemetry` (§3.6, v2 — no server endpoint, no env writing), the
opencode/pi TypeScript shims (§3.4, v2), the skill-usage feedback loop (v2,
gated on Q024.4), a cursor adapter (Q024.3), `lode doctor` capability
checks (Q024.1). Parts 2–3 cover §3.3 and §3.5.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/harness/harness.go` (new) | `Event`, `Harness` interface, `HookInstall`/`HookUninstall`/`SkillTarget`, scope consts, registry |
| `internal/harness/jsonfile.go` (new) | shared read/write of generic JSON config files (moved from `claude.go`) |
| `internal/harness/claudecode.go` (new) | the claude-code adapter — `claude.go`'s bindings + merge machinery, verbatim |
| `internal/harness/codex.go` (new) | codex adapter: `~/.codex/hooks.json` entry lists |
| `internal/harness/copilot.go` (new) | copilot adapter: `~/.copilot/hooks/worklode.json`, a file we own outright |
| `internal/harness/amp.go` (new) | amp adapter: `amp.hooks` array in the Amp settings JSON |
| `internal/harness/*_test.go` (new) | per-adapter tests; claude cases migrated from `internal/cmd/claude_test.go` |
| `internal/cmd/claude.go` | deleted (contents moved); `internal/cmd/claude_test.go` deleted (cases moved) |
| `internal/cmd/install.go` | multi-agent targets, per-agent report with `unbound_events` |
| `internal/cmd/install_test.go` | updated flag-resolution and report tests |
| `internal/cmd/instructions.go` (new) | `AGENTS.md` managed block + `CLAUDE.md` `@AGENTS.md` bootstrap |
| `internal/cmd/instructions_test.go` (new) | block idempotence, foreign-content preservation, uninstall |
| `internal/cmd/hook.go` | parse `--harness <id>` alongside `--next` |
| `internal/cmd/hook_test.go` (new) | `parseHookArgs` cases (currently untested) |
| `internal/hookrun/hookrun.go` | `Options.Harness`, harness-aware `agentName` |
| `internal/hookrun/normalize.go` (new) | per-harness payload normalization |
| `internal/hookrun/normalize_test.go` (new) | fixture payloads per harness |
| `deploy/base/migrations/0011_agent_harnesses.{up,down}.sql` (new) | widen the `agent_sessions.agent` CHECK |
| `deploy/base/kustomization.yaml` | list the new migration pair |

**Test commands:** `go test ./internal/harness/ ./internal/cmd/ ./internal/hookrun/`
(no Postgres needed); `go test ./internal/store/` for the migration
round-trip (needs Postgres with pgvector, `TEST_POSTGRES_DSN` to override).
Commit after every task, imperative mood, no trailers.

---

## Tasks

### Task 1 — Build `internal/harness`: types, registry, claude-code adapter

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/harness/harness.go`, `internal/harness/jsonfile.go`, `internal/harness/claudecode.go`, `internal/harness/harness_test.go`, `internal/harness/claudecode_test.go`
- Delete: `internal/cmd/claude.go`, `internal/cmd/claude_test.go`
- Modify: `internal/cmd/install.go` (call sites only — full reshape is Task 2)

- [ ] **Step 1: Write the failing registry + adapter test**

`internal/harness/harness_test.go`:

```go
package harness

import "testing"

func TestRegistryKnowsClaudeCode(t *testing.T) {
	h, ok := Get("claude-code")
	if !ok || h.ID() != "claude-code" {
		t.Fatalf("Get(claude-code) = %v, %v", h, ok)
	}
	if _, ok := Get("opencode"); ok {
		t.Fatal("opencode has no v1 adapter and must not be registered")
	}
	ids := IDs()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("IDs() not sorted: %v", ids)
		}
	}
}

func TestEventsCoverHeartbeat(t *testing.T) {
	// Every v1 adapter must map Heartbeat or report it unbound — it is the
	// portability payoff (spec 024 §2.5). claude-code maps it to four events.
	h, _ := Get("claude-code")
	if got := h.Events()[Heartbeat]; len(got) != 4 {
		t.Fatalf("claude-code Heartbeat events = %v; want 4", got)
	}
}
```

`internal/harness/claudecode_test.go`: move every test from
`internal/cmd/claude_test.go` (`TestClaudeInstallScopes`,
`TestClaudeInstallWritesBindings`,
`TestClaudeInstallDoesNotBindDelegationHooks`,
`TestClaudeInstallIsIdempotentAndPreservesForeignSettings`,
`TestClaudeUninstallWithNoSettingsFile`,
`TestClaudeUninstallNoopLeavesFileUntouched`) plus the `readSettings` and
`commandsFor` helpers, package `harness`, calling the adapter methods
(`(&ClaudeCode{}).InstallHooks(repoDir, ScopeLocal)` etc.) instead of the cmd
functions. The assertions do not change — behaviour is moved, not modified.

Run: `go test ./internal/harness/` — FAIL (package does not exist).

- [ ] **Step 2: Implement `harness.go`**

```go
// Package harness holds one adapter per coding-agent harness plus a registry
// (spec 024 §3.1). An adapter is a table of locations and event names; the
// behaviour behind them is always `lode hook <event>`, so no adapter ever
// introduces a second coordination model.
package harness

import "sort"

// Settings scopes, moved from internal/cmd: local settings are the
// developer's own (git-ignored), project settings are committed. Adapters
// whose native config has no such split treat both scopes alike.
const (
	ScopeLocal   = "local"
	ScopeProject = "project"
)

// Event is Worklode's lifecycle vocabulary. PreCommit is deliberately not an
// adapter concern: the git hook covers it for every harness (spec 024 §3.4).
type Event string

const (
	SessionStart  Event = "session-start"
	SessionEnd    Event = "session-end"
	Heartbeat     Event = "heartbeat"
	WorktreeEnter Event = "worktree-enter"
)

// AllEvents is the fixed order reports use.
var AllEvents = []Event{SessionStart, SessionEnd, Heartbeat, WorktreeEnter}

// SkillTarget is one directory a harness reads skills from. PerSkill means
// link <Dir>/<name> per skill (the directory is user-owned and must not be
// replaced wholesale); otherwise Dir itself may be created as a symlink to
// the Worklode skills dir.
type SkillTarget struct {
	Dir      string
	PerSkill bool
}

// HookInstall reports what one adapter's InstallHooks wrote. Unbound names
// the Worklode events this harness cannot express — degraded coverage,
// never an install failure (spec 024 §3.1).
type HookInstall struct {
	Path    string
	Bound   []string // harness-native event names actually bound
	Unbound []Event
}

// HookUninstall mirrors internal/cmd's git-hook action vocabulary.
type HookUninstall struct {
	Path   string
	Action string // "removed" | "none"
}

const (
	ActionRemoved = "removed"
	ActionNone    = "none"
)

// Harness is one coding agent's integration surface (spec 024 §3.1).
type Harness interface {
	ID() string
	// Detect reports whether this harness is configured for repoDir or the
	// user, so `--agent auto` installs only what is actually in use.
	Detect(repoDir string) (bool, error)
	SkillTargets(repoDir, scope string) ([]SkillTarget, error)
	InstallHooks(repoDir, scope string) (HookInstall, error)
	UninstallHooks(repoDir, scope string) (HookUninstall, error)
	Events() map[Event][]string
}

var registry = map[string]Harness{}

func register(h Harness) { registry[h.ID()] = h }

// Get returns the adapter for id.
func Get(id string) (Harness, bool) { h, ok := registry[id]; return h, ok }

// IDs returns every registered adapter id, sorted.
func IDs() []string {
	out := make([]string, 0, len(registry))
	for id := range registry {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Detected returns the ids (sorted) whose Detect fires for repoDir. A
// Detect error skips that adapter — auto-detection must never fail install.
func Detected(repoDir string) []string {
	var out []string
	for _, id := range IDs() {
		if ok, err := registry[id].Detect(repoDir); err == nil && ok {
			out = append(out, id)
		}
	}
	return out
}
```

- [ ] **Step 3: Move the Claude Code machinery**

`internal/harness/jsonfile.go` gets `readJSONFile`/`writeJSONFile` — the
bodies of `readSettingsFile`/`writeSettingsFile` from
`internal/cmd/claude.go:86-120`, renamed, unchanged (missing file ⇒ empty
object; indented, newline-terminated output).

`internal/harness/claudecode.go` gets the rest of `claude.go` verbatim —
`lodeHookPrefix`, `claudeBinding`, `claudeBindings` (with its full doc
comment, including the WorktreeCreate/WorktreeRemove rationale),
`claudeSettingsPath`, `installClaudeHooks` → method, `uninstallClaudeHooks`
→ method, `settingsHooks`, `appendBinding`, `stripLodeHooks`,
`isLodeHookEntry` — wrapped as:

```go
// ClaudeCode is the claude-code adapter: JSON hook bindings in
// .claude/settings*.json. Its command strings stay `lode hook <event>` with
// no --harness flag: claude-code is the default harness, and the bare
// prefix is what makes uninstall recognize bindings from installs that
// predate this package.
type ClaudeCode struct{}

func (ClaudeCode) ID() string { return "claude-code" }

// Detect: a .claude directory in the repo, or Claude Code configured for
// the user (~/.claude exists).
func (ClaudeCode) Detect(repoDir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoDir, ".claude")); err == nil {
		return true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(home, ".claude"))
	return err == nil, nil
}

// SkillTargets: ~/.claude/skills, per-skill — the directory is user-owned
// (spec 024 §3.3). Claude Code reads no project-scope shared dir.
func (ClaudeCode) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".claude", "skills"), PerSkill: true}}, nil
}

func (ClaudeCode) Events() map[Event][]string {
	return map[Event][]string{
		SessionStart:  {"SessionStart"},
		SessionEnd:    {"SessionEnd"},
		Heartbeat:     {"Stop", "StopFailure", "SubagentStop", "Notification"},
		WorktreeEnter: {"PostToolUse:EnterWorktree"},
	}
}

func init() { register(ClaudeCode{}) }
```

`InstallHooks(repoDir, scope)` resolves the settings path itself (the
`worktree.Root` + `claudeSettingsPath` composition currently in
`settingsPathForScope`, `claude.go:63`), runs the old `installClaudeHooks`
body, and returns `HookInstall{Path: path, Bound: [the 7 native names],
Unbound: nil}`. `UninstallHooks` wraps `uninstallClaudeHooks` the same way,
mapping its action strings onto `ActionRemoved`/`ActionNone`.

Delete `internal/cmd/claude.go` and `internal/cmd/claude_test.go`. In
`internal/cmd/install.go`, patch the two call sites minimally so the package
compiles: `installClaudeHooks(path)` →
`harness.ClaudeCode{}.InstallHooks(dir, scope)` etc., and replace the local
`scopeLocal` const uses with `harness.ScopeLocal` (Task 2 reworks this file
properly).

- [ ] **Step 4: Verify**

```bash
go test ./internal/harness/ ./internal/cmd/ -count=1
```

Every moved claude test passes unmodified in its new home; `install_test.go`
still passes (behaviour-preserving refactor).

- [ ] **Step 5: Commit**

```bash
git add internal/harness internal/cmd
git commit -m "Extract the Claude Code integration into internal/harness"
```

---

### Task 2 — Grow `lode install`'s harness dimension

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/cmd/install.go`, `internal/cmd/install_test.go`

- [ ] **Step 1: Write the failing tests**

Extend `internal/cmd/install_test.go` (the `targetsFor` helper keeps
working — `--agent` becomes a `StringSlice`, which `Flags().Parse` handles):

```go
func TestResolveHookTargetsRepeatableAgent(t *testing.T) {
	got, err := targetsFor(t, "--agent", "claude-code", "--agent", "codex")
	if err != nil {
		t.Fatalf("two agents: %v", err)
	}
	if len(got.agents) != 2 || got.agents[0] != "claude-code" || got.agents[1] != "codex" {
		t.Fatalf("agents = %v", got.agents)
	}
	// Duplicates collapse.
	got, err = targetsFor(t, "--agent", "codex", "--agent", "codex")
	if err != nil || len(got.agents) != 1 {
		t.Fatalf("dedupe: %v %v", got.agents, err)
	}
}

func TestResolveHookTargetsAgentAll(t *testing.T) {
	got, err := targetsFor(t, "--agent", "all")
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if len(got.agents) != len(harness.IDs()) {
		t.Fatalf("all = %v; want every registered id", got.agents)
	}
	// all combined with an explicit id is a contradiction of intent.
	if _, err := targetsFor(t, "--agent", "all", "--agent", "codex"); err == nil {
		t.Fatal("all + explicit id accepted")
	}
}

func TestResolveHookTargetsRejectsUnknownAgent(t *testing.T) {
	_, err := targetsFor(t, "--agent", "opencode")
	if err == nil || !strings.Contains(err.Error(), "claude-code") {
		t.Fatalf("unknown agent error must list supported ids, got %v", err)
	}
}
```

Note: `auto` cannot resolve inside `targetsFor` (no repo); resolution of
`auto` happens in `installHooks` against `dir`, so `targetsFor(t)` (defaults)
now yields `agents == []string{"auto"}` — update
`TestResolveHookTargetsDefaults` accordingly, and update
`TestResolveHookTargetsRejectsContradictions` for the slice flag.

Add a report test:

```go
func TestInstallReportsPerAgentWithUnboundEvents(t *testing.T) {
	// In a temp git repo with a .claude dir (so auto detects claude-code),
	// run installHooks with targets{vcs: "", agents: ["claude-code"]} and
	// assert the JSON shape: res.Agents[0].Agent == "claude-code",
	// res.Agents[0].Path ends with .claude/settings.local.json, and
	// res.Agents[0].UnboundEvents is empty. (Adapters with unbound events
	// are covered in the amp task.)
}
```

Run: `go test ./internal/cmd/ -run TestResolveHookTargets` — FAIL.

- [ ] **Step 2: Implement**

In `internal/cmd/install.go`:

1. `hookTargets` becomes `struct { vcs string; agents []string }` where
   `agents` may be `["auto"]` (resolved later, against the repo dir) or a
   validated explicit list.
2. `addHookFlags`: `cmd.Flags().StringSlice("agent", []string{"auto"},
   "coding agent(s) to manage: an adapter id, auto, or all (repeatable)")`.
   Keep `--vcs`, `--no-vcs`, `--no-agent`, `--scope` as they are, but
   `--scope`'s help now names it Claude-Code-specific.
3. `resolveHookTargets`: keep the `--agent`/`--no-agent` contradiction and
   the `--no-vcs`+`--no-agent` "nothing to do" checks. Then normalize:
   `all` expands to `harness.IDs()` and must be the only value; `auto` must
   be the only value (it is the un-touched default or an explicit single
   `--agent auto`); anything else must satisfy `harness.Get`, error message
   `fmt.Sprintf("unsupported --agent %q (supported: %s, auto, all)", id,
   strings.Join(harness.IDs(), ", "))`. Dedupe preserving order.
4. `installHooks(dir, targets, scope)`: resolve `auto` first —
   `harness.Detected(dir)`; empty is not an error (spec §4 row 1: nothing is
   written for a harness that isn't there). Then per id:

```go
	for _, id := range agents {
		h, _ := harness.Get(id)
		hi, err := h.InstallHooks(dir, scope)
		if err != nil {
			return res, fmt.Errorf("install %s hooks: %w", id, err)
		}
		res.Agents = append(res.Agents, agentInstall{
			Agent: id, Path: hi.Path, Bound: hi.Bound,
			UnboundEvents: eventNames(hi.Unbound),
		})
	}
```

   with the one-liner alongside:

```go
// eventNames renders harness.Events as strings for the JSON report.
func eventNames(evs []harness.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, string(e))
	}
	return out
}
```

5. Result types:

```go
type installResult struct {
	VCS    *vcsInstall    `json:"vcs,omitempty"`
	Agents []agentInstall `json:"agents,omitempty"`
}

type agentInstall struct {
	Agent string   `json:"agent"`
	Path  string   `json:"path"`
	Bound []string `json:"bound,omitempty"`
	// UnboundEvents names the Worklode events this harness could not bind
	// (spec 024 acceptance 4). Coverage degrades to the git pre-commit
	// heartbeat, which the vcs stanza reports.
	UnboundEvents []string `json:"unbound_events,omitempty"`
}
```

   Mirror for `uninstallResult.Agents []agentUninstall`. The old singular
   `agent` JSON key is gone — spec §3.2 says the report grows into a list.
6. `reportInstall`/`reportUninstall`: loop the agents; the text line for an
   agent with unbound events reads
   `codex: installed hooks in <path> (no binding for: worktree-enter; git pre-commit still covers the heartbeat)`.
7. Update `newInstallCmd`/`newUninstallCmd` long help: name `auto`/`all`,
   note that an explicitly named harness installs even when undetected
   (asking for it is the detection signal, spec §3.2).

- [ ] **Step 3: Verify**

```bash
go test ./internal/cmd/ -count=1
```

Also run the e2e-adjacent manual check: in a scratch repo with a `.claude/`
dir, `go run . install --json` reports one `agents` entry; re-running
converges; `go run . uninstall --json` restores.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd
git commit -m "Make lode install's --agent repeatable with auto and all"
```

---

### Task 3 — Widen the `agent_sessions.agent` CHECK

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `deploy/base/migrations/0011_agent_harnesses.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`
- Test: `internal/store/agent_sessions_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/store/agent_sessions_test.go`, following the file's
existing lease/claim fixture pattern (claim a task, then touch a session)
— the assertion that matters:

```go
func TestAgentSessionAcceptsCopilot(t *testing.T) {
	// Spec 024 adds copilot as a harness; 0004's CHECK predates it. Touch
	// must not violate agent_sessions_agent_known.
	// ... claim fixture as in the surrounding tests ...
	if _, err := s.TouchAgentSession(ctx, taskID, actorID, "copilot", "", "sess-copilot"); err != nil {
		t.Fatalf("touch as copilot: %v", err)
	}
}
```

Run: `go test ./internal/store/ -run TestAgentSessionAcceptsCopilot`
Expected: FAIL — CHECK violation (needs Postgres; skips silently without
one, so run it against the compose stack).

- [ ] **Step 2: Write the migration pair**

`deploy/base/migrations/0011_agent_harnesses.up.sql`:

```sql
-- Spec 024: copilot is an integratable harness; 0004's CHECK predates it.
-- Never edit a shipped migration — replace the constraint.
ALTER TABLE agent_sessions DROP CONSTRAINT agent_sessions_agent_known;
ALTER TABLE agent_sessions ADD CONSTRAINT agent_sessions_agent_known CHECK (agent IN
    ('claude-code','codex','copilot','cursor','aider',
     'opencode','pi','amp','other'));
```

`.down.sql` restores 0004's original list verbatim.

List both files in the `worklode-migrations` `configMapGenerator` in
`deploy/base/kustomization.yaml`, after the 0009 entries.

- [ ] **Step 3: Verify**

```bash
./scripts/check-migrations.sh --no-fix
go test ./internal/store/ -run 'TestMigrate|TestAgentSession' -count=1
```

- [ ] **Step 4: Commit**

```bash
git add deploy/base internal/store
git commit -m "Accept copilot in the agent_sessions CHECK"
```

---

### Task 4 — Add `--harness` payload normalization to `lode hook`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `internal/cmd/hook.go`, `internal/hookrun/hookrun.go`
- Create: `internal/cmd/hook_test.go`, `internal/hookrun/normalize.go`, `internal/hookrun/normalize_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/cmd/hook_test.go` — `parseHookArgs` has no tests today; cover the
existing contract plus the new flag:

```go
func TestParseHookArgs(t *testing.T) {
	cases := []struct {
		args    []string
		event   string
		harness string
		next    []string
		wantErr bool
	}{
		{args: []string{"heartbeat"}, event: "heartbeat", harness: ""},
		{args: []string{"heartbeat", "--harness", "codex"}, event: "heartbeat", harness: "codex"},
		{args: []string{"heartbeat", "--next", "other-hook", "--harness", "x"},
			event: "heartbeat", next: []string{"other-hook", "--harness", "x"}},
		// --harness before --next: both parsed, the argv after --next verbatim.
		{args: []string{"pre-commit", "--harness", "copilot", "--next", "pre-commit"},
			event: "pre-commit", harness: "copilot", next: []string{"pre-commit"}},
		{args: []string{"heartbeat", "--harness"}, wantErr: true},
		{args: nil, wantErr: true},
	}
	// assert event, harness, next, err for each
}
```

`internal/hookrun/normalize_test.go` — fixture payloads per harness
(best current knowledge of each harness's field names; the shapes are what
the adapter tasks' verification steps confirm):

```go
func TestNormalizePayload(t *testing.T) {
	cases := []struct {
		harness string
		raw     string
		want    Payload
	}{
		// claude-code: the existing shape, byte-identical behaviour.
		{"claude-code",
			`{"cwd":"/w","session_id":"s1","transcript_path":"/t.jsonl"}`,
			Payload{Cwd: "/w", SessionID: "s1", TranscriptPath: "/t.jsonl"}},
		// The default (empty harness) is claude-code, for every binding
		// already installed (spec 024 §3.4).
		{"", `{"cwd":"/w","session_id":"s1"}`, Payload{Cwd: "/w", SessionID: "s1"}},
		// codex: camelCase keys.
		{"codex", `{"cwd":"/w","sessionId":"s2"}`, Payload{Cwd: "/w", SessionID: "s2"}},
		// copilot: camelCase with workingDirectory.
		{"copilot", `{"workingDirectory":"/w","sessionId":"s3"}`, Payload{Cwd: "/w", SessionID: "s3"}},
		// A field Worklode needs but the payload omits ⇒ zero value ⇒ the
		// guard NOPs. Never an error (spec 024 §3.4).
		{"amp", `{"unrelated":true}`, Payload{}},
		{"codex", `not json`, Payload{}},
	}
	// assert normalizePayload(c.harness, []byte(c.raw)) == c.want
}
```

Also add one end-to-end case to `internal/hookrun/hookrun_test.go`, next to
the existing session-start auto-resume test (the fake-client fixture with an
expired lease): drive `Run` with `Options{Event: "session-start", Harness:
"codex"}` and a **camelCase** payload (`{"cwd": <wt>, "sessionId": "s9"}`),
and assert the lease is re-acquired and the session touched exactly as the
Claude Code shape does — spec 024 acceptance 3's "same behaviour, same
backbone rows, different payload on stdin".

Run both — FAIL (`normalizePayload` undefined; `parseHookArgs` has the
wrong signature).

- [ ] **Step 2: Implement**

`internal/cmd/hook.go`: `parseHookArgs` returns
`(event, harnessID string, next []string, err error)`. Scan `args[1:]`:
`--next` ends the scan as today (everything after is verbatim);
`--harness` consumes the following token (error when absent). Pass
`Harness: harnessID` in `hookrun.Options`. Update the `Use` string to
`hook <event> [--harness <id>] [--next <cmd> [arg...]]`.

`internal/hookrun/normalize.go`:

```go
// normalizePayload maps one harness's hook payload onto hookrun's internal
// Payload before the guard runs (spec 024 §3.4). Unknown harnesses and
// missing fields degrade to zero values — the guard then NOPs; a payload
// never produces a user-visible error.
func normalizePayload(harnessID string, raw []byte) Payload {
	var p Payload
	_ = json.Unmarshal(raw, &p) // canonical (claude-code) field names first
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return p
	}
	pick := func(dst *string, keys ...string) {
		if *dst != "" {
			return
		}
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				*dst = v
				return
			}
		}
	}
	pick(&p.Cwd, "workingDirectory", "working_directory", "workspacePath", "workspace_path")
	pick(&p.SessionID, "sessionId", "session", "threadId", "thread_id")
	pick(&p.TranscriptPath, "transcriptPath", "rolloutPath", "rollout_path")
	return p
}
```

In `hookrun.go`: add `Harness string` to `Options`; `Run` replaces its two
unmarshal lines with `payload := normalizePayload(opts.Harness, raw)`.
Replace the package-level `agentName()` with a method:

```go
// agentName is the agent recorded on agent_sessions rows: LODE_AGENT
// overrides, then the --harness id, then claude-code (the default for every
// binding installed before the flag existed).
func (o Options) agentName() string {
	if a := os.Getenv("LODE_AGENT"); a != "" {
		return a
	}
	if o.Harness != "" {
		return o.Harness
	}
	return "claude-code"
}
```

and thread `opts` to the two call sites (`reportSession`, `endSession` —
`hookrun.go:251,285`).

- [ ] **Step 3: Verify**

```bash
go test ./internal/cmd/ ./internal/hookrun/ -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd internal/hookrun
git commit -m "Add --harness payload normalization to lode hook"
```

---

### Task 5 — Add the codex adapter

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `internal/harness/codex.go`, `internal/harness/codex_test.go`

- [ ] **Step 1: Re-verify the format**

Check `codex-rs/hooks` and `docs/config.md` in
[openai/codex](https://github.com/openai/codex) (spec 024 §8): the
`hooks.json` location (`~/.codex/hooks.json`, `$CODEX_HOME` honored), the
per-event entry-list shape, and the event names
(`SessionStart`, `SessionEnd`, `Stop`, `SubagentStop` — spec §2.3). If the
shape differs from Step 2's, adjust the adapter and its fixtures to the
verified shape and say so in the commit message. If `hooks.json` cannot
carry a plain command handler, stop and escalate — that invalidates the
spec's §2.3 findings, not just this task.

- [ ] **Step 2: Write the failing test**

`internal/harness/codex_test.go`, using a `t.Setenv("CODEX_HOME", t.TempDir())`
so no real config is touched:

```go
func TestCodexInstallUninstallRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	// Foreign content that must survive: a top-level key and a foreign
	// entry on an event we also bind.
	seed := `{"other":{"keep":true},"hooks":{"SessionStart":[{"type":"command","command":"their-hook"}]}}`
	os.WriteFile(filepath.Join(home, "hooks.json"), []byte(seed), 0o644)

	h, _ := Get("codex")
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if hi.Path != filepath.Join(home, "hooks.json") {
		t.Fatalf("path = %s", hi.Path)
	}
	if len(hi.Unbound) != 1 || hi.Unbound[0] != WorktreeEnter {
		t.Fatalf("unbound = %v; want [worktree-enter]", hi.Unbound)
	}
	// Re-run converges: same bytes.
	before, _ := os.ReadFile(hi.Path)
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	after, _ := os.ReadFile(hi.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("reinstall did not converge")
	}
	// Bindings present, --harness codex on every command, foreign entry kept.
	var cfg map[string]any
	json.Unmarshal(after, &cfg)
	// ... assert cfg["other"] survived; assert each of SessionStart,
	// SessionEnd, Stop, SubagentStop has exactly one entry whose command
	// starts "lode hook " and ends "--harness codex"; assert the foreign
	// SessionStart entry is still there ...

	// Uninstall restores the seed semantically (foreign entry back alone).
	hu, err := h.UninstallHooks(t.TempDir(), ScopeLocal)
	if err != nil || hu.Action != ActionRemoved {
		t.Fatalf("uninstall: %+v %v", hu, err)
	}
	// second uninstall: ActionNone, file untouched (compare mtimes or bytes)
}

func TestCodexRefusesUnroundtrippableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	os.WriteFile(filepath.Join(home, "hooks.json"), []byte("not json"), 0o644)
	h, _ := Get("codex")
	if _, err := h.InstallHooks(t.TempDir(), ScopeLocal); err == nil {
		t.Fatal("unparseable config was rewritten") // spec 024 acceptance 6
	}
}
```

Run — FAIL (`codex` unregistered).

- [ ] **Step 3: Implement `codex.go`**

```go
// Codex is the codex adapter: entry lists in $CODEX_HOME/hooks.json
// (default ~/.codex/hooks.json). Both scopes write the user layer — the
// `lode hook` guard is what scopes behaviour to Worklode worktrees.
type Codex struct{}

func (Codex) ID() string { return "codex" }

func codexHome() (string, error) {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func (Codex) Detect(repoDir string) (bool, error) {
	dir, err := codexHome()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(dir)
	return err == nil, nil
}

func (Codex) SkillTargets(repoDir, scope string) ([]SkillTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []SkillTarget{{Dir: filepath.Join(home, ".agents", "skills")}}, nil
}

func (Codex) Events() map[Event][]string {
	return map[Event][]string{
		SessionStart: {"SessionStart"},
		SessionEnd:   {"SessionEnd"},
		Heartbeat:    {"Stop", "SubagentStop"},
		// WorktreeEnter: unmapped — codex has no EnterWorktree tool event.
	}
}
```

`InstallHooks`: `readJSONFile(path)`; take `cfg["hooks"]` as
`map[string]any` of entry lists; strip entries whose `command` string has
the `lodeHookPrefix` (reuse `stripLodeHooks` — it already operates on the
generic shape? No: claude's shape nests groups. Write a flat-list variant
`stripLodeEntries(list []any) ([]any, bool)` next to it and share
`isLodeHookEntry`); then for each `(event, natives)` in `Events()` append
`{"type": "command", "command": "lode hook <event> --harness codex"}` per
native name; write back; return the `HookInstall` with
`Unbound: missingEvents(Codex{})` where

```go
// missingEvents lists AllEvents entries absent from h.Events().
func missingEvents(h Harness) []Event
```

goes in `harness.go` (every adapter reports the same way). A file that
exists but does not parse returns the read error unwrapped — never a
rewrite (acceptance 6). `UninstallHooks` strips and reports
`ActionNone` without rewriting when nothing was stripped (mirror
`uninstallClaudeHooks`'s no-op contract).

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/harness/ -run Codex -count=1 && go test ./internal/harness/
git add internal/harness
git commit -m "Add the codex harness adapter"
```

---

### Task 6 — Add the copilot adapter

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1, 3]
```

**Files:**
- Create: `internal/harness/copilot.go`, `internal/harness/copilot_test.go`

- [ ] **Step 1: Re-verify the format**

Check the GitHub Docs hooks page (spec 024 §8: "hooks" under Copilot
concepts/agents): personal hooks dir `~/.copilot/hooks/*.json`, repo hooks
dir `.github/hooks/*.json`, event names (`sessionStart`, `sessionEnd`,
`agentStop`, `subagentStop`, `preToolUse`, `postToolUse`,
`userPromptSubmitted`, `errorOccurred`), and the handler shape ("one `bash`
and one `powershell` key per handler, plus `cwd`, `env`, `timeoutSec`" —
spec §2.3). Adjust Step 2's shape to what the docs actually show; the
adapter's structure (a whole file Worklode owns) does not change.

- [ ] **Step 2: Write the failing test**

`internal/harness/copilot_test.go`. Copilot is the one v1 adapter where
Worklode owns a whole file (`worklode.json`), so coexistence is trivial —
assert instead that foreign sibling files are untouched:

```go
func TestCopilotInstallWritesOwnedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // adapter resolves ~/.copilot via UserHomeDir
	hooksDir := filepath.Join(home, ".copilot", "hooks")
	os.MkdirAll(hooksDir, 0o755)
	foreign := filepath.Join(hooksDir, "theirs.json")
	os.WriteFile(foreign, []byte(`{"keep":true}`), 0o644)

	h, _ := Get("copilot")
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if hi.Path != filepath.Join(hooksDir, "worklode.json") {
		t.Fatalf("path = %s", hi.Path)
	}
	if len(hi.Unbound) != 1 || hi.Unbound[0] != WorktreeEnter {
		t.Fatalf("unbound = %v", hi.Unbound)
	}
	// ... decode worklode.json: sessionStart/sessionEnd/agentStop/
	// subagentStop handlers whose bash command starts "lode hook " and
	// carries "--harness copilot" ...
	// foreign file byte-identical; uninstall deletes only worklode.json,
	// second uninstall reports ActionNone.
}

func TestCopilotProjectScopeWritesRepoHooks(t *testing.T) {
	// ScopeProject → <repo>/.github/hooks/worklode.json (committed layer,
	// mirrors Claude Code's project scope).
}
```

Run — FAIL.

- [ ] **Step 3: Implement `copilot.go`**

Same skeleton as codex. `Detect`: `~/.copilot` exists, or the repo has
`.github/copilot-instructions.md`. `SkillTargets`:
`~/.agents/skills` (shared) plus nothing per-repo in v1. Path:
`ScopeProject` → `filepath.Join(repoDir, ".github", "hooks",
"worklode.json")`; `ScopeLocal` → `~/.copilot/hooks/worklode.json`.
`Events()`:

```go
	SessionStart: {"sessionStart"},
	SessionEnd:   {"sessionEnd"},
	Heartbeat:    {"agentStop", "subagentStop"},
```

File content (owned outright — uninstall deletes it, spec §3.4's
one-file-ownership rule stated for the shims applies here too):

```json
{
  "hooks": {
    "sessionStart": [{"bash": "lode hook session-start --harness copilot"}],
    "sessionEnd":   [{"bash": "lode hook session-end --harness copilot"}],
    "agentStop":    [{"bash": "lode hook heartbeat --harness copilot"}],
    "subagentStop": [{"bash": "lode hook heartbeat --harness copilot"}]
  }
}
```

Write it with `writeJSONFile` from a built map (stable key order via
MarshalIndent of a struct, so re-install converges byte-identically).
`UninstallHooks`: `os.Remove` the file; `ErrNotExist` ⇒ `ActionNone`.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/harness/ -count=1
git add internal/harness
git commit -m "Add the copilot harness adapter"
```

---

### Task 7 — Add the amp adapter

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `internal/harness/amp.go`, `internal/harness/amp_test.go`

- [ ] **Step 1: Re-verify the format — this one gates the task's shape**

Amp's hook mechanism comes from a secondary source only (spec §2 marks the
cells; §8 cites the Owner's Manual). Confirm from
[ampcode.com/manual](https://ampcode.com/manual): the settings file
(`~/.config/amp/settings.json`, `$AMP_SETTINGS_FILE` override), the
`amp.hooks` array of `{event, action}`, the `tool:post-execute` event, and
— decisive — whether an action can execute a shell command. Two outcomes:

- **Command action exists:** implement Step 3 as written (possibly with the
  verified action shape).
- **No command action:** Amp cannot reach `lode hook`. Implement the
  adapter with `Events()` empty and `InstallHooks` writing nothing,
  returning `HookInstall{Path: settingsPath, Unbound: AllEvents}` — skills
  and the git heartbeat still work (spec §4 row 2), and the install report
  says exactly that. Keep `Detect` and `SkillTargets`.

- [ ] **Step 2: Write the failing test**

`internal/harness/amp_test.go`, `t.Setenv("AMP_SETTINGS_FILE", path)`:

```go
func TestAmpInstallPreservesSettingsArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("AMP_SETTINGS_FILE", path)
	seed := `{"amp.url":"https://ampcode.com","amp.hooks":[{"event":"tool:pre-execute","action":{"type":"send-user-message","message":"theirs"}}]}`
	os.WriteFile(path, []byte(seed), 0o644)

	h, _ := Get("amp")
	hi, err := h.InstallHooks(t.TempDir(), ScopeLocal)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// Heartbeat bound via tool:post-execute; session-start/end and
	// worktree-enter unbound (spec §3.4's event map row for Amp).
	if len(hi.Unbound) != 3 {
		t.Fatalf("unbound = %v", hi.Unbound)
	}
	// ... decode: amp.url survives, the foreign hook survives, exactly one
	// entry whose action command starts "lode hook heartbeat --harness amp";
	// re-install converges; uninstall strips only ours ...
}
```

Run — FAIL.

- [ ] **Step 3: Implement `amp.go`**

Settings path: `$AMP_SETTINGS_FILE`, else `~/.config/amp/settings.json`.
`Detect`: that file exists. `SkillTargets`: `~/.agents/skills` (Table 1's
Amp row is unverified for `.amp/skills`, so v1 relies on the shared dir
only). `Events()`: `Heartbeat: {"tool:post-execute"}`. `InstallHooks`
operates on the `amp.hooks` array: strip elements whose action command has
`lodeHookPrefix`, append

```json
{"event": "tool:post-execute",
 "action": {"type": "command", "command": "lode hook heartbeat --harness amp"}}
```

write back with `writeJSONFile`, return
`Unbound: missingEvents(Amp{})` (= session-start, session-end,
worktree-enter). Unparseable settings ⇒ error, never a rewrite.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/harness/ -count=1
git add internal/harness
git commit -m "Add the amp harness adapter"
```

---

### Task 8 — Write the `AGENTS.md` managed block

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Create: `internal/cmd/instructions.go`, `internal/cmd/instructions_test.go`
- Modify: `internal/cmd/install.go` (wire into install/uninstall + report)

- [ ] **Step 1: Write the failing test**

`internal/cmd/instructions_test.go`:

```go
func TestEnsureAgentsMDCreatesAndConverges(t *testing.T) {
	root := t.TempDir()
	action, err := ensureAgentsMD(root)
	if err != nil || action != "created" {
		t.Fatalf("first run: %s %v", action, err)
	}
	first, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !bytes.Contains(first, []byte("lode next")) {
		t.Fatalf("block does not name the entry command: %s", first)
	}
	action, err = ensureAgentsMD(root)
	if err != nil || action != "unchanged" {
		t.Fatalf("second run: %s %v", action, err)
	}
}

func TestEnsureAgentsMDPreservesForeignContent(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("# Ours\n\nHand-written.\n"), 0o644)
	if _, err := ensureAgentsMD(root); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !bytes.Contains(got, []byte("Hand-written.")) {
		t.Fatal("foreign content lost")
	}
	// A stale block is replaced in place, not duplicated: corrupt the
	// block body, re-run, assert exactly one begin marker.
}

func TestEnsureClaudeMD(t *testing.T) {
	root := t.TempDir()
	action, _ := ensureClaudeMD(root)
	if action != "created" {
		t.Fatalf("no CLAUDE.md: %s", action)
	}
	got, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if strings.TrimSpace(string(got)) != "@AGENTS.md" {
		t.Fatalf("CLAUDE.md = %q", got)
	}
	// Existing CLAUDE.md: authored prose, never edited (spec 024 §3.7).
	os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Mine\n"), 0o644)
	action, _ = ensureClaudeMD(root)
	if action != "suggested" {
		t.Fatalf("existing CLAUDE.md: %s", action)
	}
}

func TestRemoveAgentsBlock(t *testing.T) {
	// Block-only file ⇒ file deleted; mixed file ⇒ block removed, rest
	// byte-identical; no block ⇒ "none", file untouched.
}
```

Run — FAIL.

- [ ] **Step 2: Implement `instructions.go`**

```go
const (
	agentsBlockBegin = "<!-- worklode:begin — managed by `lode install`; edits inside are overwritten -->"
	agentsBlockEnd   = "<!-- worklode:end -->"
)

// agentsBlock is the two facts an agent needs before a brief exists
// (spec 024 §3.7). Deliberately short: the brief, not this file, carries
// task context.
const agentsBlock = agentsBlockBegin + `
This repository is tracked by Worklode (` + "`lode`" + `). Work is entered by
claiming a task: ` + "`lode next`" + ` claims the highest-ranked ready task and
creates its ` + "`wt/<task-id>-<slug>`" + ` worktree; ` + "`lode resume <dir>`" + `
re-enters an existing one. Run ` + "`lode status`" + ` inside a worktree to see
the current task.
` + agentsBlockEnd + "\n"
```

`ensureAgentsMD(root) (action string, err error)`: read `AGENTS.md`
(missing ⇒ empty); if a begin marker exists, splice the region between the
markers (inclusive) with the current block; else append (with a separating
blank line when the file is non-empty); write only when bytes changed;
actions `created`/`updated`/`unchanged`.

`ensureClaudeMD(root)`: missing ⇒ write `"@AGENTS.md\n"`, action
`created`; present ⇒ action `suggested` (the caller prints
`claude-code reads CLAUDE.md; add "@AGENTS.md" to it to import the
Worklode block`) — never edit an existing `CLAUDE.md`.

`removeAgentsBlock(root) (action string, err error)` for uninstall: strip
the marker region; delete `AGENTS.md` when what remains is whitespace
**and** delete a `CLAUDE.md` that is exactly `@AGENTS.md`; otherwise write
the stripped remainder.

Wire into `installHooks` (after the agents loop, unconditionally — the
block is repo-level, not per-harness; skipped only under `--no-agent` with
`--no-vcs`, i.e. never reached) and `uninstallHooks`; extend
`installResult` with
`Instructions *instructionsResult \`json:"instructions,omitempty"\``
carrying `{AgentsMD, ClaudeMD string}` actions, and one text line each in
the reports. `installHooks` takes the repo root via `worktree.Root(dir)`
(as `settingsPathForScope` did); outside a git repo, skip with a warning
rather than failing the whole install.

- [ ] **Step 3: Verify**

```bash
go test ./internal/cmd/ -count=1
go build ./... && go vet ./...
```

Manual: in a scratch repo, `go run . install` then `cat AGENTS.md CLAUDE.md`;
re-run; `go run . uninstall` removes both again.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd
git commit -m "Write the AGENTS.md managed block on install"
```

---

## Done when (part 1)

1. `lode install` in a repo where both `.claude/` and `~/.codex/` exist
   writes both configs from one command, preserves every foreign entry,
   re-runs byte-identically, and `lode uninstall` restores both
   (acceptance 1).
2. `lode install --json` lists one stanza per agent with `unbound_events`
   naming what that harness could not bind (acceptance 4's reporting half).
3. `lode hook heartbeat --harness codex` with a camelCase payload reaches
   the same guard and handlers as the Claude Code shape; outside a
   `wt/<id>-<slug>` worktree it is a NOP either way.
4. `agent_sessions` accepts `copilot`;
   `./scripts/check-migrations.sh --no-fix` passes.
5. `AGENTS.md` carries the managed block; a hand-written `CLAUDE.md` is
   never edited.
6. `go test ./...` is green and no adapter rewrites a config file it could
   not parse (acceptance 6).
