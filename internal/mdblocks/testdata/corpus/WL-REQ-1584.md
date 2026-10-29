
`lode install` binds a `commit-msg` git hook that stamps `Worklode-Task: <id>` into any commit made inside a task worktree, resolving the id from the worktree's git config with no backbone call. The trailer is the only correlation signal that survives rebase, cherry-pick and squash. The hook stamps only a message that already has a body, skips while `MERGE_HEAD` is set, and leaves an existing trailer alone.

