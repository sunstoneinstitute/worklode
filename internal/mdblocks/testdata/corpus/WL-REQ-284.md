
| Command | Action |
|---|---|
| `/lode:next [task-id] [--project P] [--kind K,K] [--strict-focus]` | Claim the top-ranked ready task (or that id), create the worktree, bind the lease, inject the brief. The one way to enter Worklode mode. |
| `/lode:resume [worktree-dir]` | Re-acquire the lease for an existing worktree and continue from its brief |
| `/lode:done` | Submit the task for review (`lode work submit`) and release the lease |
| `/lode:block --on <task-id>` | Record the blocking task and release the lease |
| `/lode:status` | Current worktree's task, lease state, heartbeat freshness. Read-only. |

These are user-invoked skills in the `lode` Claude Code plugin; the model cannot invoke them. Renewal has no slash command. Each wraps `lode work ... --json`; judgment lives in the skills. The `lode work` group holds `next`, `resume`, `submit`, `block`, `status`, `listen`. The plugin's full skill and agent list is in WL-SPEC-81 §7.5.

