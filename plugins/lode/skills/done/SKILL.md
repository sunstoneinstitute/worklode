---
name: done
description: Mark the current Worklode task done and release its lease
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Before running anything, verify the Deliverable is actually met (see
working-under-worklode: done means the definition-of-done holds, not "code
written"). If it is not met, say what's missing and stop.

Then run `lode done --json`, report the result, and surface the printed
worktree-cleanup instruction to the user.
