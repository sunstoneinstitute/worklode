
The merge probe is the client-side check, run in the task's worktree by the manual close, for whether the task's branch has landed on the default branch. It is heuristic, so squashes, stale worktrees and queued merges can produce false negatives.

