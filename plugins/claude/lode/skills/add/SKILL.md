---
name: add
description: File a new Worklode task from a short problem description, after sorting out its kind and confirming a summary
argument-hint: "<what is wrong or what needs doing>"
disable-model-invocation: true
allowed-tools: Bash(lode *)
---

Invocation arguments: $ARGUMENTS

The arguments are the user's description of the problem. Turn it into one task.
Do not start the work; this command only files it.

**Step 1: pick the kind.** `bug`, `feature`, `chore`, `design`, `review`,
`spike` or `decision`. Infer it from the description — something broken is a
`bug`, something new is a `feature`, repetitive upkeep is a `chore`, an open
question to answer before building is a `spike`, a posed question whose answer
gets recorded on the task is a `decision`. Ask the user only when the
description genuinely fits two kinds.

The eighth kind, `rally`, is not filed here: it carries no work of its own,
only `blocks` edges naming tasks that already exist. Assemble one by hand
(`lode task add --kind rally --draft`, then `lode task block`).

**Step 2: fill the gap the kind cares about.** Each kind has one thing a task
is much weaker without. Ask for it if the description does not already carry it,
in one round of questions, and accept "I don't know" as an answer.

- **bug** — how to reproduce it: what was run, what happened, what was expected.
  A bug with no repro is still worth filing; say in the body that the repro is
  missing so whoever picks it up knows to find one first.
- **feature** — for anything beyond a small, obvious change, a spec. Check
  whether one exists (`lode doc list --kind spec`). If it does, name it in
  the body. If it does not and the work is non-trivial,
  say so and offer to file a `design` task for the spec instead, or alongside.
- **chore** — whether it can be automated away. Repetitive manual upkeep is
  worth a line in the body about what would remove the need to repeat it.
- others — no extra question; the description is enough.

Check for an existing task on the same problem before filing
(`lode task list --kind <kind>`) and point the user at it rather than filing a
near-duplicate.

**Step 3: draft, then confirm.** Show the user the title, kind, priority,
project and body you intend to file, and ask them to confirm or correct it.
Do not run `lode task add` before they answer.

**Step 4: file it.**

```bash
lode task add --title "<title>" --kind <kind> --body-file - --json <<'BODY'
<body>
BODY
```

Add `--priority`, `--project`, `--parent` or `--follow-up-to` when they apply.
Report the new task's id.
