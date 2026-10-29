
- Who removes the worktree after `/lode:done`: the plugin or the human's finishing-a-branch flow.
- How a hook detects an already-running session in this worktree without a model call; the backbone's `last_seen_at` (§8.2) is the candidate signal.
- Whether to ship an MCP shim, wrapping the same commands, for clients that cannot drive a CLI.
- Whether `lode doctor` should verify each adapter's config assumptions against the installed harness version, given the capability tables change monthly.
- Whether a skill-usage feedback loop from `claude_code.skill_activated` can work at all for unregistered Worklode skills, or needs a `PostToolUse` hook on reads under the skill store path.
- Whether engine 2's unscoped candidate set is small enough to run without `--since` at real task counts, and whether reconcile should become a scheduled server loop.
- Whether `worktree.branch` is recoverable from the status-line payload for hook-created worktrees, or only from cwd.

