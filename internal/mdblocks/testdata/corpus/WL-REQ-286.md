
`lode-worker` (`plugins/claude/lode/agents/lode-worker.md`) is a headless subagent for unattended loops: run a selection rule (default `lode work next --json`), claim, do the work through one subagent per task, submit or block, repeat until nothing is ready. `/lode:start-agent-loop` starts it. `lode work listen` waits until there is claimable work. It is safe because the claim is atomic, the lease is worktree-bound, and the heartbeat is commit-driven, so many workers run in parallel on a well-specified project. It is optional; the plugin works fully in interactive sessions.

