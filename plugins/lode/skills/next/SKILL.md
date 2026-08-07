---
name: next
description: Claim the next ready Worklode task (or a specific one), create its worktree, and start working in it
argument-hint: "[task-id] [--project P] [--strict-focus]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

## Claim result
!`lode next $ARGUMENTS --json`

If `claimed` is false: tell the user nothing is ready and stop.
Otherwise a worktree was created and the lease is bound to it. cd into the
`worktree` path from the JSON, read the `brief`, and start the task. The brief
is the context contract — do NOT spelunk the repo to reconstruct context; if
the brief is insufficient, say so: the task likely needs decomposition.
Load the working-under-worklode skill before starting.
