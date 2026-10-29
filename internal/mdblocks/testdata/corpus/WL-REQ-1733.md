
`extensions/worklode.ts`, in the Pi package of WL-REQ-293, registers event handlers and commands, runs `lode` with `ctx.cwd` under a short timeout and Pi's cancellation signal, and turns failures into UI notifications. Every call is best effort and never blocks Pi from starting, ending a turn, or shutting down. A non-Worklode directory and an idle task are normal states that clear the status. The extension owns one identity, `lode`, for all UI registrations and clears its status on shutdown.

| Pi event | Worklode action |
|---|---|
| `session_start` | `lode-hook session-start` |
| `session_shutdown` | `lode-hook session-end` (also fires for reload, new-session, resume, fork, clone) |
| `turn_end` | `lode-hook heartbeat` (chosen over `agent_end` because a run may auto-retry) |
| `agent_settled` | refresh status only |

User-only commands `/lode:next [task]`, `/lode:resume [task]`, `/lode:done`, `/lode:block [reason]`, `/lode:status` delegate to the corresponding `lode work` commands and refresh status. No model-callable tool is exposed. Pi-native guidance under `plugins/pi/lode/skills/` duplicates the operational prose of the Claude tree without Claude frontmatter. After session start, every command, and `agent_settled`, the extension runs `lode work status --json` and renders task key, title, lease, and heartbeat health with `ctx.ui.setStatus("lode", value)`, guarded by `ctx.hasUI`. Status refreshes may contact the backbone and so never sit on streaming events.

Acceptance: one Pi session in a worktree yields session-start, heartbeat, and session-end facts without a model instruction; Pi stays usable if any lifecycle or status call fails. TypeScript tests cover argument construction, failure rendering, event mapping, status parsing, UI guards, and status clearing.

