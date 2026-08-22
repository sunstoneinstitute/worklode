---
name: spec-coverage
description: Find accepted specs with sections no accepted plan covers and create Worklode planning tasks for them
argument-hint: "[--project P] [--dry-run]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Invocation arguments: $ARGUMENTS

Ask the backbone, which owns the answer:

```bash
lode doc list --needs-planning --json      # add --project <P> if $ARGUMENTS gave one
```

The response is `{"docs":[…], "planning_gaps":[…]}`: one `docs` entry per
**accepted spec** with at least one section no accepted plan discharges, and a
matching `planning_gaps` entry keyed by document id — `{"doc":…,
"sections":<the spec's section count>, "gaps":[{"anchor":"sec-3",
"coverage":"unplanned|partial|bound-only|deferred"}]}`. A `deferred` gap also
carries `"owner"`: the document a plan explicitly handed the section to
(026 §5.3) — someone was named, nothing is scheduled, so it is the first kind
of gap to chase.

**What this reports, and why.** The whole `--needs-planning` result, coverage
classification included — that selector *is* the backbone's definition of a
planning gap (026 §2.1), and this skill exists to turn gaps into planning
tasks. It is wider than the old file-corpus check, which reported only specs
with zero referencing plans and treated section-level partial debt as a plan's
own declared debt. That case is still visible here: it is a spec whose gap set
covers every section (`len(gaps) == sections`) with every `coverage` equal to
`unplanned`. Lead the report with those — nothing has been planned at all — and
list the rest under them as partial debt, so a spec with one uncovered section
is not mistaken for an unplanned spec.

For each gap, check whether it is already tracked: `lode task list --status
all --json` and look for a `kind: design` task whose title starts with `Plan
spec <N> —` (the convention already in use, e.g. `WL-22`). If one exists,
report it instead of creating a duplicate.

Report the full gap list to the user: spec number, title, which sections are
uncovered and at what classification, and whether it is already tracked.

If `--dry-run` is in $ARGUMENTS, stop here.

Otherwise, for every untracked gap, run:

```
lode task add --kind design \
  --title "Plan spec <N> — <title>" \
  --body "Write an implementation plan covering <KEY>-SPEC-<N> (<slug>), sections <anchors>. Read it with 'lode show <KEY>-SPEC-<N>'. See the splitting-specs-into-plans skill." \
  --project <P> --json
```

`<KEY>` is the project's own key — `project_key` in `.worklode/config.toml`,
`WL` in the worklode repo itself. Never hardcode another repo's key.

using `--project` only if $ARGUMENTS gave one (otherwise `lode task add`
defaults to the current repo's project). Print each created task's id.

This only queues the task to *write* a plan — never draft or outline the plan
itself here. Spec → plan decomposition is an explicit human act; this skill
offers it by creating the claimable task, it does not perform it.
