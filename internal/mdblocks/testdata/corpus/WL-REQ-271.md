
`git worktree move` and `git branch -m` alone do not make a moved worktree usable: the lease still names the old `<host>:<path>` identity and `cli.ReacquireOrRenew` refuses to resume a lease held by a different worktree. No command rebinds an existing lease; `RebindWorktree` runs only from `lode work next`. The sequence is: move, rename the branch, `lode task release <id>`, then `lode work resume <new>`.

