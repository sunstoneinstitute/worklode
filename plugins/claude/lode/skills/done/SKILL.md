---
name: done
description: Submit the current Worklode task for review and release its lease
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *) Bash(gh *)
---

Before running anything, verify the Deliverable is actually met (see
working-under-worklode: done means the definition-of-done holds, not "code
written"). If it is not met, say what's missing and stop.

Then commit, push the branch, open its PR, and arm auto-merge on it:

```bash
gh pr merge --auto --squash
```

That hands the PR to the repo's merge queue, which lands it once its checks
pass with no human in the path (docs/github-advanced-setup.md). `lode work
submit` does none of this, and the task's move to `merged` comes from the
PR-merge webhook, not from you.

Finally run `lode work submit --json`, which submits the task for review
(`in_review`) and releases the lease. Report the result and surface the
printed worktree-cleanup instruction to the user.
