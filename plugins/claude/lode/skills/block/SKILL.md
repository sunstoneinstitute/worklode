---
name: block
description: Record a real blocker on the current Worklode task and release its lease
argument-hint: "--on <blocker-task-id>"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

First confirm a real blocker exists (see working-under-worklode: block only
when progress needs a decision or artifact outside this task's scope, not for
minor in-scope obstacles). If it is not a genuine blocker, say so and stop.

If the blocker is a task that already exists, note its id. If it does not exist
yet, create it first with `lode task add …` and use the new task's id.

Then run `lode block --on <blocker-id> --json`, report the result to the user.
