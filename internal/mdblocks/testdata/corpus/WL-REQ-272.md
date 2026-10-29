
Every lease-bearing hook is a NOP outside a Worklode worktree: absent guard, `exit 0` immediately. The guard is per handler. Event names below are Claude Code's; WL-REQ-278 maps them per harness.

| Event | Action | Guard |
|---|---|---|
| `SessionStart` (in worktree) | Resume: re-acquire the lease if expired or renew it if close to expiry, open the agent session, inject the brief | In a Worklode worktree. Never a silent claim. |
| `PostToolUse` on `EnterWorktree` | Open an agent session on the entered worktree's lease (WL-REQ-291) | In a Worklode worktree |
| `SessionStart` (outside worktree) | Offer to resume an abandoned worktree | A flat scan of the worktree base (up to five entries) finds a worktree whose lease is expired or absent and whose session marker is stale. Offer only, naming `/lode:resume <dir>`. |
| `pre-commit` (git) | Renew the lease (the heartbeat) | In a Worklode worktree whose lease this worktree holds. Never re-claims, never blocks the commit. |
| `SessionEnd` | Close the agent session; the lease stays | In a git worktree |
| `worktree-create` / `worktree-remove` | Re-acquire an abandoned worktree's lease / release the lease | Path under the worktree base. No adapter binds either, by design (WL-REQ-265). |
| `commit-msg` (git) | Stamp `Worklode-Task: <id>` into the message | In a Worklode worktree, no merge in progress, message has a body. Takes no lease and makes no backbone call; the id comes from `worklode.task-id` on disk. |
| `commit-msg` (git) | Reject a message that cites a spec section, naming the rule ref to use (WL-REQ-1791) | Every checkout of the repository. Runs before the stamp. |
| `SessionStart`, `SubagentStart` | Inject the instruction to cite rules by ref (WL-REQ-1791) | None: every checkout |
| `post-merge`, `post-commit` (git) | Report the merge: name the tasks whose branches this commit brought in | HEAD is the repo's default branch. NOP before any network call otherwise. |

Notes:

- Inside a worktree, starting a session re-acquires the bound lease automatically. Outside, the session only gets an offer. Acquisition stays deliberate.
- Renewal is a heartbeat driven by commits. A stalled session stops committing and its lease ages out to the sweeper.
- `commit-msg` rather than `prepare-commit-msg`, so the "only stamp a message that already has a body" gate preserves the empty-message abort for interactive commits.

