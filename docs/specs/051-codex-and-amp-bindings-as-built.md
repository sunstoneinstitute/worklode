---
status: draft
issued: 2026-08-22
kind: adr
requires:
- 008-worklode-plugin.md
amends:
  "#sec-1":
  - 008-worklode-plugin.md#sec-16.3
  - 008-worklode-plugin.md#sec-17.4
  "#sec-2":
  - 008-worklode-plugin.md#sec-17.2
  - 008-worklode-plugin.md#sec-17.4
  "#sec-3":
  - 008-worklode-plugin.md#sec-17.4
  "#sec-4":
  - 008-worklode-plugin.md#sec-17.4
  "#sec-5":
  - 008-worklode-plugin.md#sec-17.7
---
# ADR 051 — Codex and Amp event bindings, as built

## 0. Context {#sec-0}

The multi-harness format re-verification (2026-08-20) found that spec 008's
harness survey (§16.3) and hook-delivery design (§17.2, §17.4, §17.7) had
drifted from the verified surfaces. The v1 adapters were built to the verified
reality, and the implementation-delta rule forbade editing the spec
mid-execution, so the divergence was recorded nowhere in the corpus. This ADR
records it. `internal/harness/codex.go` and `internal/harness/amp.go` are the
authorities these corrections restate.

## 1. Codex defines `SessionEnd`, and the adapter binds it {#sec-1}

008 §16.3 claims Codex has "no `SessionEnd` event", and its closing paragraph
builds on that: clean Codex session shutdown supposedly degrades to the next
reconciliation path "rather than binding a fictional `SessionEnd` event". Both
are wrong. Codex's hook schema accepts `SessionEnd` alongside the ten events
the row lists, and the codex adapter binds it
(`lode hook session-end --harness codex`), so clean session shutdown is
observed directly and nothing degrades to reconciliation. The paragraph's
remaining caution stands: an accepted event name still does not imply uniform
runtime coverage across Codex releases, and `lode doctor` verification against
the installed version is still called for.

## 2. Amp is a generated plugin, not a settings array {#sec-2}

008 §17.2's coexistence rule lists "a settings array (Amp)" among the formats
adapters restate the never-clobber rule in. That was never built, and cannot
be: Amp's `amp.hooks` actions can only send a user message or redact tool
input — no action runs a shell command, so no settings entry can reach
`lode hook`. The amp adapter instead uses Amp's **Plugin API**: a TypeScript
file rendered whole from the adapter's own binding table into
`<amp config dir>/plugins/worklode.ts`, making Amp the **third code-generating
adapter** beside the opencode and pi shims 008 §17.4 names. For coexistence it
belongs in the "file we own outright" class: install rewrites it, uninstall
deletes it (only when it carries Worklode's marker), and Amp's
`settings.json` — which the adapter still reads for `Detect` — is untouched in
both directions.

## 3. Amp's lifecycle bindings, and the tool events left unbound {#sec-3}

Through that plugin, Amp binds `SessionStart` to `session.start` and
`Heartbeat` to both ends of a turn — `agent.start` and `agent.end` — not to
`tool:post-execute` as 008 §17.4's event map claimed.

`tool.call`/`tool.result` are **deliberately unbound and stay that way**. Both
are request events on Amp's agent critical path — a handler runs before the
tool executes, and again before its result reaches the model — so binding them
would put a `lode hook` subprocess between the agent and every tool call,
dozens of times a turn, to report a heartbeat `agent.end` already reports.
This is a decision, not a gap.

`SessionEnd` stays unbound for a different reason: Amp's Plugin API has no
session-end event at all. That is Amp's ceiling, not an install falling short;
the git `pre-commit` heartbeat remains the coverage floor, exactly as 008
§17.4 already provides.

## 4. The event map, corrected {#sec-4}

008 §17.4's event map with the Codex and Amp columns as built; the other
columns are restated unchanged (Pi's delivery is amended separately by spec
041 §2):

| Worklode event | Claude Code | Codex | Copilot | Amp | opencode / pi (shim) |
|---|---|---|---|---|---|
| `SessionStart` | `SessionStart` | `SessionStart` | `sessionStart` | `session.start` | `session.*` / `session_start` |
| `SessionEnd` | `SessionEnd` | `SessionEnd` | `sessionEnd` | — | `session_shutdown` |
| `Heartbeat` | `Stop`, `StopFailure`, `SubagentStop`, `Notification` | `Stop`, `SubagentStop` | `agentStop`, `subagentStop` | `agent.start`, `agent.end` | `session.idle` / agent events |
| `WorktreeEnter` | `PostToolUse:EnterWorktree` | — | — | — | — |
| `PreCommit` | (git hook) | (git hook) | (git hook) | (git hook) | (git hook) |

## 5. Work is entered through `lode worktree next` first {#sec-5}

008 §17.7 says the managed `AGENTS.md` block states "that work is entered
through `lode task claim`". The block that shipped leads with `lode worktree next` —
which claims the top-ranked ready task *and* creates its worktree, what an
agent with no brief actually needs — and names `lode task claim <id>` second,
as the way to claim a specific task. The block matches the intent; the spec's
sentence is corrected to describe it.

## 6. What each harness does with session-start stdout {#sec-6}

The session-start hook's stdout is the task brief — the payload the binding
exists to deliver — and its consumption is per-harness (WL-287).
`lode hook session-start` therefore emits per `--harness`, and the statement
of record is:

| Harness | Session-start stdout | Emitted |
|---|---|---|
| Claude Code | injected via the documented `hookSpecificOutput.additionalContext` envelope | the envelope |
| Amp | the generated plugin captures the shell result's `stdout` and returns it from the thread's first `agent.start` as a context message — the one Plugin API slot that reaches the agent; held per thread id, delivered once (a restarted plugin process re-delivers once, harmless) | plain text |
| Codex | injected via `hookSpecificOutput.additionalContext` — the same envelope, verified against codex-cli 0.147.0 (WL-303) | the envelope |
| Copilot | **contract documented but unverified**, and a *different* shape — see below | nothing |

**Codex was verified, not assumed (WL-303).** Codex's hooks are `Stage::Stable`
and default-enabled at 0.147.0, and its `session-start.command.output` schema —
shipped inside the installed binary and checked in at the release tag — declares
`hookSpecificOutput.{hookEventName,additionalContext}`, identical to Claude
Code's. Verification was end to end, not schema reading alone: a `SessionStart`
hook emitting the envelope with a distinctive marker, run through a real
`codex exec` session, and the model asked to repeat the marker — it did. So
`emitSessionContext` now falls through to the same envelope for Codex, and
Codex sessions open with the brief.

Two properties of that schema are load-bearing and constrain the envelope
permanently:

- `additionalProperties: false` at both levels, and stdout that *looks* like
  JSON but fails the schema is a hard error that **drops the context
  entirely** — there is no fallback to plain text. An extra key is therefore
  not a cosmetic diff; it silently costs Codex the whole brief. The
  `internal/hookrun` test asserts the exact key set for this reason.
- Non-JSON stdout is injected verbatim as developer context, so plain text is
  the safer wire if the schema ever drifts. The envelope is kept only because
  it is verified against this version.

Codex's trust gate applies as §1 notes: hooks stay skipped until reviewed with
`/hooks`, so the brief arrives only once the binding is trusted.

**Copilot's ceiling is evidentiary, not technical.** GitHub Copilot CLI does
document a `sessionStart` hook that injects context, but Worklode does not emit
it, for two independent reasons. First, the envelope is *not* the Claude/Codex
one — it is flat, `{"additionalContext": "..."}` with no `hookSpecificOutput`
wrapper, and non-JSON stdout is discarded rather than injected, so no single
emission can serve both Copilot and Codex. Second, and decisively, no Copilot
CLI was installed to verify against, and the vendor's own documentation
contradicts itself — the hooks reference says `additionalContext` is injected
while the CLI tutorial says output is ignored — over a feature that has already
regressed once after being fixed. Shipping on documentation alone here is
exactly the unverified envelope §0 forbids. Copilot sessions consequently still
open without the brief; the binding renews the lease and opens the session,
which is what it is bound for. Wiring it is a small change once a Copilot CLI
exists to run the same marker test against: add a `copilot` arm emitting the
flat shape, and assert it the way the Codex arm is asserted.
