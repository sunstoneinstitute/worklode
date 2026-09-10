---
name: next
description: Claim the next ready Worklode task (or a specific one), create its worktree, and start working in it
argument-hint: "[task-id] [--project P] [--kind K,K] [--strict-focus]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Invocation arguments: $ARGUMENTS

Run `lode work next --json`, adding only the parts of those arguments that are
genuine CLI input: an optional task id, `--project <key>`, `--kind <list>`,
`--strict-focus`.
`lode work next` takes at most one positional argument, so anything else the user
typed is context for the work, not command input — never pass it to the
command; carry it into the task instead and mention you did.

## Which kinds to ask for

`--kind` takes a comma-separated list. When the user did not name one, default
it from the tier this session is running on (`MODEL_SELECTION.md`), so an
escalated design task is not claimed by the loop that could not resolve it:

| This session's tier | Default |
|---|---|
| Mechanical (Sonnet, GPT-5.6-Terra) | `--kind feature,bug,chore` |
| High (Opus, Fable, GPT-5.6-Sol) | `--kind design,spike,review` |

A tier you cannot determine takes no default: pass no `--kind` and claim from
the whole ready set.

If the project has an active rally, its members sort ahead of everything
else, so the task you get back may not be the one the board ranks first. A
rally is never itself claimed.

If `claimed` is false: tell the user nothing is ready and stop.
Otherwise a worktree was created and the lease is bound to it. cd into the
`worktree` path from the JSON, read the `brief`, and start the task. The brief
is the context contract — do NOT spelunk the repo to reconstruct context; if
the brief is insufficient, say so: the task likely needs decomposition.
Load the working-under-worklode skill before starting.
