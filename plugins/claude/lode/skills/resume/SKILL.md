---
name: resume
description: Re-acquire the Worklode task bound to the current (or given) worktree and continue from its brief
argument-hint: "[worktree-dir]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Invocation arguments: $ARGUMENTS

Run `lode worktree resume --json`, adding only a worktree directory if those arguments
name one. `lode worktree resume` takes at most one positional argument, so anything else
the user typed is context for the work, not command input — never pass it to
the command; carry it into the task instead and mention you did.

Re-acquire this worktree's task. Report what state it was in (lease renewed, or
re-claimed after the sweeper reclaimed it). Then continue from the `brief` in
the JSON — the brief is the context contract; do NOT spelunk the repo to
reconstruct context. If the brief is insufficient, say so: the task likely
needs decomposition. Load the working-under-worklode skill before continuing.
