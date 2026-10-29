
- The question direction for the MCP relay: a pending-question row, an answer surface, a worker-side wait, and a `PreToolUse` hook redirecting `AskUserQuestion` to it.
- Where worker images live and who may pull them. The registry's reachability rides the egress baseline.
- Whether the sandbox provider guarantees a setup step before the agent session starts. If not, the image is the only supported sandbox path.
- What builds a project's `.worklode/Dockerfile` on first use, and who pays for the wait.
- Whether Codex log records carry a session id its hooks can register, so they attribute the same way as Claude Code's (WL-REQ-1730).
