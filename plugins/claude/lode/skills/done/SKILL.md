---
name: done
description: Submit the current Worklode task for review and release its lease
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *) Bash(gh *)
---

Before running anything, verify the Deliverable is actually met (see
working-under-worklode: done means the definition-of-done holds, not "code
written"). If it is not met, say what's missing and stop.

Then commit, push the branch, and open its PR.

Decide whether the change needs a human reviewer (WL-REQ-1325). It does when
it adds a migration or changes how stored data is written or read; touches
authentication, authorization, tokens, secrets or the route guard table;
changes deploy manifests, CI workflows or release configuration; removes or
renames a CLI verb, an API route or a wire field; departs from a governing
rule or settles a design question its plan left open; or leaves a doubt your
own review could not resolve. The list is a floor: you may open a review for
another reason. If any applies, open one from the worktree, saying why:

```bash
lode review add <task> --reason "<why a human should look>"
```

That moves the task to `in_review`. Do not arm auto-merge: the change merges
after an `approved` verdict (WL-REQ-1779). Otherwise open no review and arm
auto-merge:

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
