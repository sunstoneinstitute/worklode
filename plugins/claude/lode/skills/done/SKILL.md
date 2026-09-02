---
name: done
description: Submit the current Worklode task for review and release its lease
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Before running anything, verify the Deliverable is actually met (see
working-under-worklode: done means the definition-of-done holds, not "code
written"). If it is not met, say what's missing and stop.

Then commit, push the branch, and open its PR — `lode work done` does neither, and
the task's move to `merged` comes from the PR-merge webhook, not from you.

Finally run `lode work done --json`, which submits the task for review
(`in_review`) and releases the lease. Report the result and surface the
printed worktree-cleanup instruction to the user.
