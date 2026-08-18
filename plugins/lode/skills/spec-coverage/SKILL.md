---
name: spec-coverage
description: Find specs no plan covers and create Worklode planning tasks for them
argument-hint: "[--project P] [--dry-run]"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *) Bash(python3 *)
---

Invocation arguments: $ARGUMENTS

Run `python3 plugins/lode/skills/spec-coverage/find_gaps.py` from the repo
root. It prints one JSON object per line for every spec under `docs/specs/`
that no plan under `docs/plans/` covers — a spec is "covered" the moment any
plan's `covers:` frontmatter names it, at any section, at any level (`full`,
`partial`, or `none`). Section-level partial-coverage gaps are a plan's own
declared debt (see `docs/authoring-design-docs.md`,
`splitting-specs-into-plans`) and belong to `docs/follow-ups.md`'s rule, not
this skill — only report specs with **zero** referencing plans.

For each gap, check whether it is already tracked: `lode task list --status
all --json` and look for a `kind: spec` task whose title starts with `Plan
spec <N> —` (the convention already in use, e.g. `WL-22`). If one exists,
report it instead of creating a duplicate.

Report the full gap list to the user: spec number, title, and whether it is
already tracked.

If `--dry-run` is in $ARGUMENTS, stop here.

Otherwise, for every untracked gap, run:

```
lode task add --kind spec \
  --title "Plan spec <N> — <title>" \
  --body "Write an implementation plan covering docs/specs/<file>. See docs/authoring-design-docs.md and the splitting-specs-into-plans skill." \
  --project <P> --json
```

using `--project` only if $ARGUMENTS gave one (otherwise `lode task add`
defaults to the current repo's project). Print each created task's id.

This only queues the task to *write* a plan — never draft or outline the plan
itself here. Spec → plan decomposition is an explicit human act; this skill
offers it by creating the claimable task, it does not perform it.
