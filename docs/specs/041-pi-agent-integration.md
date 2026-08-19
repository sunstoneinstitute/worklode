---
status: draft
requires:
  - 008-worklode-plugin.md
  - 012-agent-sessions.md
  - 016-org-wide-skills.md
amends:
  "#sec-2":
    - 008-worklode-plugin.md#sec-17.4
  "#sec-4":
    - 008-worklode-plugin.md#sec-17.5
---
# Spec 041 — Pi agent integration

## 0. Purpose & scope {#sec-0}

Pi is the first non-Claude harness Worklode integrates through a native,
distributable extension rather than a shell-hook configuration. This spec
delivers the complete Worklode lifecycle in Pi: session tracking, heartbeats,
the task commands, task guidance, and a live status indicator. It is intended
to expose differences between Pi's event model and Claude Code's hooks while
leaving the coordination model in 008 unchanged.

The adapter owns no task, lease, worktree, or authentication state. The `lode`
binary remains the sole interface to Worklode; Pi owns ChatGPT sign-in and its
own project-trust decision. The first release is installed from this repository
as a project-local package. Publishing the package to npm is explicitly later
work, not a different integration design.

Out of scope: OpenAI authentication, transcript/cost ingestion, a Pi provider,
automatic task claiming, and a general cross-harness skill source format.

## 1. Package boundary and installation {#sec-1}

The Pi surface lives at `plugins/pi/lode/` and is a self-contained Pi package:

```text
plugins/pi/lode/
  package.json
  extensions/worklode.ts
  skills/
  README.md
```

`package.json` declares the extension and skills through its `pi` manifest.
The extension imports Pi's declared peer packages only; it has no runtime
dependency on a separately installed Worklode JavaScript package. This makes
the directory valid both as a local package now and as a publishable package
later.

`lode install --agent pi` resolves the repository root and safely merges the
relative package source `./plugins/pi/lode` into that root's
`.pi/settings.json`. A relative Pi package source is resolved relative to the
settings file, so every checkout uses its matching Worklode package. The
installer preserves all other settings and package entries, recognizes the
same resolved local path as its own on a rerun, and reports the package source
in its normal install result. `lode uninstall --agent pi` removes only that
package entry and leaves `.pi/settings.json` untouched when no Worklode entry
is present.

Pi loads project-local packages only after the user has trusted the project.
The installer must neither pre-approve that trust nor invoke Pi's login flow.
If Pi is unavailable, the explicit `--agent pi` install reports a prerequisite;
automatic detection may skip it without changing configuration.

## 2. Extension adapter {#sec-2}

`extensions/worklode.ts` is a thin adapter around `lode`. It registers Pi
event handlers and slash commands, executes `lode` with `ctx.cwd`, captures
stdout/stderr, and turns failures into Pi UI notifications. It neither
recreates Worklode's state machine nor persists a shadow copy of a task or
lease in the Pi session.

Each lifecycle call is best effort. A non-zero result, missing `lode` binary,
or malformed status response is visible to the user but never blocks Pi from
starting, ending a turn, or shutting down. The adapter applies a short timeout
and Pi's cancellation signal to subprocesses. It treats a non-Worklode
directory and an idle task as normal states, clearing the status rather than
emitting an error.

The adapter owns one extension identity, `lode`, for all UI registrations. It
must clear the status it owns on session shutdown; it never replaces another
extension's footer or widget.

## 3. Lifecycle mapping {#sec-3}

Pi's events map to Worklode's existing hook vocabulary as follows:

| Pi event | Worklode action | Reason |
|---|---|---|
| `session_start` | `lode hook session-start` | records or resumes the session in the current worktree |
| `session_shutdown` | `lode hook session-end` | closes the session even when Pi exits or replaces it |
| `turn_end` | `lode hook heartbeat` | renews after every completed model/tool turn |
| `agent_settled` | refresh status only | Pi documents this as the point with no automatic retry, compaction, or queued follow-up remaining |

`turn_end`, rather than `agent_end`, is the heartbeat source because an agent
run may auto-retry or continue. `agent_settled` is deliberately not a second
heartbeat: status needs the settled boundary, but duplicate lifecycle writes
would obscure the event model being evaluated. The existing git pre-commit
heartbeat remains the harness-independent floor from 008.

Pi may emit `session_shutdown` for reload, new-session, resume, fork, and
clone flows as well as process exit. The extension sends the ordinary
session-end hook for each such shutdown. The hook layer remains responsible
for making repeated or reordered lifecycle signals harmless.

## 4. Commands, skills, and status {#sec-4}

The extension registers these user-only commands: `/lode-next [task]`,
`/lode-resume [task]`, `/lode-done`, `/lode-block [reason]`, and
`/lode-status`. Their handlers invoke the existing corresponding `lode`
commands, render their normal human-readable result in Pi, and refresh status.
They do not expose a model-callable tool: claiming and releasing a lease stay
explicit user decisions, consistent with 008's slash-command contract.

Pi-native guidance lives under `plugins/pi/lode/skills/`. It covers the same
operations and done/block judgment as the Claude tree, but is not a symlink or
a second source of truth for Claude metadata. Claude-specific frontmatter,
namespaced command invocation, and agent definitions do not describe Pi. The
first release intentionally duplicates this small operational prose. A future
shared source is justified only after stable common fragments are demonstrated
in both renderings.

After session start, every command, and `agent_settled`, the extension runs
`lode status --json` and renders a compact task key/title plus lease and
heartbeat health using `ctx.ui.setStatus("lode", value)`. The status is a
Pi-owned footer item, not a `lode statusline` subprocess binding. It is
guarded by `ctx.hasUI`; print and JSON modes still perform lifecycle hooks but
make no UI calls. A status refresh may contact the backbone and is therefore
not placed on streaming or message-update events.

## 5. Discovery criteria {#sec-5}

The integration is deliberately full-lifecycle so implementation resolves,
rather than assumes, these seams:

1. whether `turn_end` emits reliably after tool failures, aborts, and
   compaction/retry paths, and whether the heartbeat cadence is appropriate;
2. whether session replacement/fork semantics create the desired session rows
   without prematurely treating a worktree lease as released;
3. whether Pi subagents produce independent events sufficient for session and
   heartbeat attribution;
4. how local package paths behave in linked Worklode worktrees, including
   project trust and Pi reload/update behavior;
5. whether Pi skill names or command names collide with project resources; and
6. the latency and failure behaviour of a `lode status --json` refresh in Pi's
   TUI, RPC, print, and JSON modes.

Each result becomes either an implementation test, a documented degradation,
or an amendment to this spec. It is not acceptable to silently substitute
prompt instructions for a missing deterministic lifecycle signal.

## 6. Testing and acceptance criteria {#sec-6}

The Go suite covers Pi selection, detection, and an idempotent,
non-destructive `.pi/settings.json` package merge/removal. It includes an
existing foreign package, a duplicate Worklode local path, invalid JSON, and a
settings file that becomes empty after uninstall.

The Pi package has TypeScript tests for command argument construction, command
failure rendering, event-to-hook mapping, status parsing, UI-mode guards, and
status clearing. A manual acceptance script records the real Pi version and
tests project trust, package loading, `/reload`, a Worklode worktree session,
a normal turn, an aborted/failed turn, a session switch, and each `/lode-*`
command.

The integration is acceptable when:

1. `lode install --agent pi` and uninstall preserve foreign Pi settings and
   converge on repeated runs;
2. a trusted Pi project loads the local package from the matching checkout;
3. one Pi session in a Worklode worktree yields session-start, heartbeat, and
   session-end facts without a model instruction; and
4. every task command delegates to `lode`, while Pi remains usable if any
   individual lifecycle or status call fails.

## 7. Dependencies and sources {#sec-7}

This spec depends on 008 for the worktree/lease lifecycle and hook semantics,
012 for agent-session recording, and 016 for the generic skill model.

Primary sources, consulted 2026-08-19:

- Pi, [Packages](https://pi.dev/docs/latest/packages) — local package paths,
  project settings, package manifest, and trust boundary.
- Pi, [Extensions](https://pi.dev/docs/latest/extensions) — lifecycle events,
  commands, subprocess execution, status UI, and non-TUI modes.
