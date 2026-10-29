
A lease and its worktree live until the task is done. Neither is tied to a merge, a review or a deploy. The manual close ends the active lease in its own transaction (`finishTask`). A task declaring `merge` is not done until the merge landed, so the worktree survives review. A task declaring `manual` is done when the actor says so, and the worktree goes at the same moment. The sweeper keys on the lease only.

