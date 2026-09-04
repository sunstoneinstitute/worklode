---
name: working-under-worklode
description: Use when working inside a Worklode worktree (under .worktrees/) — the done/block/release judgment loop for leased tasks
---

# Working under Worklode

You are in a worktree bound to a leased task. Machinery (hooks) already
handles lease renewal, resume, and release — NEVER think about heartbeats,
renewal, or lease TTLs; committing at a normal cadence is the heartbeat.

## The three judgments that are yours

**Done** — a task is done when its definition-of-done / Deliverable holds,
not when code is written. Check the brief's definition_of_done (when null,
the task body is the contract). Tests pass, the deliverable exists where it
should. Then push the branch, open its PR, arm auto-merge on it
(`gh pr merge --auto --squash`), and run /lode:done — which submits the task
for review and releases the lease. It does not mark the task `merged`; that
lands when the merge queue lands the PR.

**Block** — block (don't push through) when progress requires a decision or
artifact outside this task's scope: a missing dependency, a design decision
someone must make, an unmet precondition. Record it honestly with
/lode:block so the frontier reflects reality. Push through minor obstacles
that are within scope.

**Release without done/block** — if you must abandon the worktree with the
task genuinely still workable (wrong fit, user redirected you), just stop;
removing the worktree releases the lease, and an untouched worktree ages out
to the sweeper. Don't mark done what isn't.

## Before you call it done

Two ways a check lies, both seen in one day (WL-357, WL-358, WL-371, WL-378):

**The deployed fix may not be the code you ran.** `lode` ships as client and
server. A change under `internal/cmd/`, `internal/designdoc/` or
`internal/hookrun/` lands in the *binary*; a task reaching `deployed_dev` says
nothing about it. Rerunning a reproduction against a stale local `lode`
reproduces the old behaviour whatever shipped — that mistake reported a correct
fix as broken, reopened the task, and cost a redundant PR. Read the diff: if it
touches the client, rebuild before you retest.

**Capability is not occurrence.** Code that *permits* a transition is not
evidence it happened that way. Reading the source for the endpoint that could
have caused a state change yields a confident wrong story just as readily as a
right one. Worklode keeps the actual record — `lode task timeline <id>`, plus `git
reflog` for anything a hook did — so ask what happened rather than what was
possible.

Both reduce to one habit: name the evidence for the claim you are about to
make. "The reproduction passes" is a claim about a binary; "a worker ran X" is
a claim about an event log. Neither is established by reading code.

## Context discipline

The brief is the context contract. If it is not enough to do the work, that
is a signal the task needs decomposition — set it with
`lode task edit <id> --needs-decomposition=true` and /lode:block or report,
rather than spelunking the repo to reverse-engineer intent.

## When you delegate

You do the leased task's work yourself — no dispatch needed. This section
applies only when the task is big enough that you fan out subagents
(decomposition, subagent-driven implementation, review): then you're a
coordinator and pick each subagent's tier. Use your harness's column.

| What you're dispatching | Claude Code | Codex |
|---|---|---|
| Fully-specified implementation (exact files/code/tests, no open design) | `model: "sonnet"` | `gpt-5.6-terra`, `medium` |
| Review, or any task with unknowns (debugging, design gaps, plan-vs-reality) | `model: "opus"` | `gpt-5.6-sol`, `high` |

- **Claude Code:** always set `model` explicitly — omitting it falls back to
  the top-level session model, silently running mechanical work on the most
  expensive tier.
- **Codex:** set both `model` and `reasoning_effort`.
- If an implementer hits ambiguity, escalate that task to the higher tier
  rather than letting it improvise.
