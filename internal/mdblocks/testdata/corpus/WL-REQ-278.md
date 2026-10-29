
| Worklode event | Claude Code | Codex | Copilot | Amp |
|---|---|---|---|---|
| `session-start` | `SessionStart` | `SessionStart` | `sessionStart` | `session.start` |
| `subagent-start` | `SubagentStart` | none | none | none |
| `session-end` | `SessionEnd` | `SessionEnd` | `sessionEnd` | none |
| `heartbeat` | `Stop`, `StopFailure`, `SubagentStop`, `Notification` | `Stop`, `SubagentStop` | `agentStop`, `subagentStop` | `agent.start`, `agent.end` |
| `worktree-enter` | `PostToolUse` matcher `EnterWorktree` | none | none | none |
| pre-commit | git hook | git hook | git hook | git hook |

Claude Code, Codex and Copilot take a shell command in JSON bindings, so the compiled binary is the whole integration. Amp takes a TypeScript plugin Worklode renders from an embedded template; it binds `session.start` and `agent.start`/`agent.end` with tool events deliberately unbound, and a re-install that would write the same bytes writes nothing. Amp has no session-end event and no `EnterWorktree` tool, so both stay unbound, and Amp loads plugins only at startup. Codex hooks stay inactive until the user approves them with `/hooks`, and need re-approval after any change; the install report says so. Codex hook coverage varies by release for tool, compaction, and subagent events; adapters bind those only where a missed delivery is tolerable. The pre-commit git hook is the coverage floor for a harness with no usable session events.

Claude Code events deliberately not bound: unmatched `PostToolUse` and `PostToolBatch` (hundreds of firings for no signal), `UserPromptSubmit` (a subset of `Stop`), `WorktreeCreate`/`WorktreeRemove` (delegation hooks that would make Worklode the worktree creator), `PreCompact`/`PostCompact` (`SessionStart` re-fires with `source: compact`), `TaskCreated`/`TaskCompleted` (Claude Code's in-session todos, unrelated to Worklode tasks).

