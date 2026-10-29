
The manual close **asserts and reports. It never refuses on a done reference.** This holds for all three reference kinds. What varies is what the close does on the way through.

| Reference | Behaviour |
|---|---|
| `merge` | Runs the local merge probe (`internal/hookrun/localmerge.go`'s `landedNow`: `git merge-base --is-ancestor`, falling back to `git cherry` for a squash) in the task's worktree. Found: reports it through `POST /api/v1/merges` exactly as the `post-merge` hook does, so the fact lands as a `task_commits` row with source `local_merge` and passes through `ResolveDelivery`. Then the task closes. Not found: the task still closes and the CLI prints a warning naming the branch, the default branch probed, and that no merge was found. |
| `manual` | Closes the task and writes only the ordinary event and `state_log` rows. No probe, no warning. |
| deliverable id | Neither probes nor reports deliverable state. Prints each declared deliverable's current reported state and warns when one the task claims to produce is still unreported. |

The merge probe warns because it is heuristic (squashes, stale worktrees and queued merges all produce false negatives) and because it runs on the client, where the clone is. The server cannot re-run it, so a server-side refusal would be the same assertion with more steps. A per-project strictness setting is deliberately not built.

The probe reuses `worklode_local_merge_reports_total{result}`. The state guard is unchanged: the manual close accepts a task in `ready`, `in_progress` or `in_review` and refuses `draft`, a terminal state, and a task with children (WL-SPEC-75 §3.2, §5.2).

