
The guard asks one string question: is this path exactly one segment below a segment equal to the configured base directory? No template, no config beyond the base, no subprocess, no network. It runs on every hook event, most of which are nowhere near a worktree. A path inside a worktree is not itself a worktree root; handlers that accept one resolve it through `worktree.Root` first.

Scanning for adoptable worktrees (`SessionStart` outside a worktree) reads the base one level deep.

