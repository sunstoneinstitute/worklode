---
description: Start a coding subagent that claims tasks via Worklode and executes them, in a loop
model: sonnet
argument-hint: "[task-id | spec-ref | bugs | unblock] [--project P] [--kind K] [--strict-focus] [--max N]"
allowed-tools: Bash(lode *) Bash(git *)
disable-model-invocation: true
---

Invocation arguments: $ARGUMENTS

Start a @lode-worker agent with the instructions below. If no agent of that
type is available, follow them yourself.

## Resolve the focus

Read $ARGUMENTS once and resolve it to exactly one **selection rule** — the
command that picks the next task on every pass of the loop. Scope flags
(`--project <key>`, `--repo <owner/name>`) apply to every rule: pass them
through verbatim. `--max <n>` stops the loop after n tasks; the default is
unbounded.

| $ARGUMENTS | Focus | Selection rule |
|---|---|---|
| nothing | any ready task | `lode worktree next --json` |
| `--kind <k>`, `--strict-focus` | narrowed frontier | the same, with those flags |
| bugs, bugfixes, chores, "clean up" | maintenance | `lode task list --status ready --kind bug --json`, then `--kind chore` once bugs run dry |
| a spec (`032`, `WL-SPEC-32`), a plan slug, or "finish <feature>" | one spec through to done | see **Spec focus** |
| unblock, unblocking, "get things moving" | clear blockers | see **Unblock focus** |
| a bare task id (`WL-429`) | that task, then stop | `lode worktree next WL-429 --json` |

Anything else in $ARGUMENTS is context for the work, not command input: never
pass it to `lode`. Carry it to each task's subagent as standing guidance, and
say that you did.

**Spec focus.** `lode doc todo <ref> --json` is the spec's execution queue,
already in dependency order. Walk it each pass:

- `unexecuted` — `lode task list --plan <that item's plan slug> --status ready
  --json`; the first task it returns is the pick.
- `unplanned`, `partial`, `plan-draft`, `blocked` — not loop work. Each needs a
  plan written or a human to accept one. Note it and move to the next item.

When no item is `unexecuted`, the loop has taken the spec as far as it can:
report what is left, by type, and stop.

**Unblock focus.** `lode task blockers --json` returns `trees`, one per blocked
task. Count how often each id appears across every `blockers` array, and pick
the `"state": "ready"` blocker with the highest count — ties break on lower
`depth`, then on list order. Recount on every pass: landing one blocker
reshapes the rest.

## The loop

1. Run the selection rule. Nothing selected, or `--max` reached: report why and
   stop.
2. Claim: `lode worktree next <id> --json` when the rule picked an id (the
   default rule claims on its own). `"claimed": false` means another worker
   took it — reselect.
3. `cd` into the JSON's `worktree`. The `brief` is the context contract; do not
   spelunk the repo to reconstruct it. A brief too thin to work from is a
   decomposition signal, not a research prompt: `lode task edit <id>
   --needs-decomposition=true`, `lode worktree block`, back to 1.
4. Dispatch **one subagent per task** to do the work in that worktree, handing
   it the worktree path, the brief verbatim, and any standing guidance from
   $ARGUMENTS. Tier it per the delegation table in the working-under-worklode
   skill. It commits, pushes the branch, and opens the PR; it does not call
   `done`. One task per subagent is what keeps this loop's own context from
   filling up over a long run.
5. Judge its report yourself — that judgment is the whole reason this loop is
   an agent and not a shell script. Confirm the commits actually landed on that
   worktree's branch (`git -C <worktree> log --oneline origin/main..HEAD`)
   before believing a DONE. Then `lode worktree done --json`, or `lode worktree
   block --on <id> --json` when the report names a real blocker. Neither
   command removes the worktree itself (`lode worktree` only ever creates
   one) — from the main repo, run `git worktree remove <worktree>` (add
   `--force` only if it refuses over untracked build artifacts, never over
   uncommitted edits) so an unattended run doesn't leave one behind per task.
   Back to 1.

Load the working-under-worklode skill before the first task.
