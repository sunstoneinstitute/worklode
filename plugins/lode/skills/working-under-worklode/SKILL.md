---
name: working-under-worklode
description: Use when working inside a Worklode worktree (wt/<id>-<slug>) — the done/block/release judgment loop for leased tasks
---

# Working under Worklode

You are in a worktree bound to a leased task. Machinery (hooks) already
handles lease renewal, resume, and release — NEVER think about heartbeats,
renewal, or lease TTLs; committing at a normal cadence is the heartbeat.

## The three judgments that are yours

**Done** — a task is done when its definition-of-done / Deliverable holds,
not when code is written. Check the brief's definition_of_done (when null,
the task body is the contract). Tests pass, the deliverable exists where it
should. Then /lode:done.

**Block** — block (don't push through) when progress requires a decision or
artifact outside this task's scope: a missing dependency, a design decision
someone must make, an unmet precondition. Record it honestly with
/lode:block so the frontier reflects reality. Push through minor obstacles
that are within scope.

**Release without done/block** — if you must abandon the worktree with the
task genuinely still workable (wrong fit, user redirected you), just stop;
removing the worktree releases the lease, and an untouched worktree ages out
to the sweeper. Don't mark done what isn't.

## Context discipline

The brief is the context contract. If it is not enough to do the work, that
is a signal the task needs decomposition — set it with
`lode task edit <id> --needs-decomposition=true` and /lode:block or report,
rather than spelunking the repo to reverse-engineer intent.
