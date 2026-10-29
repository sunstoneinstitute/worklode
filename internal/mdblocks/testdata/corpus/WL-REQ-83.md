
A Worklode task is in exactly one of these states:

| State | Meaning |
|---|---|
| `draft` | Not claimable. |
| `ready` | Claimable. |
| `in_progress` | Leased into a worktree (or started through the lease-free path). |
| `in_review` | Submitted for review. |
| `merged` | Work landed on the default branch, auto-detected or set by hand with `lode task set state merged`. For a task with children: all children delivered. |
| `completed` | Finished without landing code: the outcome is not a commit, so no PR or merge webhook will close it. Set by hand with `lode task complete`, the only way in. |
| `deployed_dev` | A dev/test deploy covers the landed commit: GitHub `deployment_status: success` and Flux `ReconciliationSucceeded`. |
| `deployed_prod` | Same, for prod. |
| `released` | A GitHub release covers the landed commit. |
| `abandoned` | Considered and dropped. Reachable only from states before `merged` and `completed`. |
