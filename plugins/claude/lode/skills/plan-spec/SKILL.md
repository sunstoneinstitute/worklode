---
name: plan-spec
description: Decompose an accepted spec into implementation plans, under a claimed design task so the planning cost bills to it
argument-hint: "<design-task-id>"
disable-model-invocation: true
allowed-tools: Bash(lode *) Bash(git *)
---

Invocation arguments: $ARGUMENTS

The first argument is the id of the `design` task to plan under. Accepting a
spec mints one (spec 025 §15.4); `lode task list --kind design` finds it if the
user did not name one.

**Step 1, before reading or writing anything: claim the task.**

```bash
lode task claim <design-task-id> --json
```

Claiming first is not bookkeeping. Agent sessions hang off leases, a lease
binds a task to a worktree, and each turn bills to the worktree it ran in
(spec 012 §4) — so planning done in the main checkout, which holds no lease,
is spent tokens nobody can attribute (025 §15.6). Claiming also gets planning
the brief, the secrets and the hook wiring every other kind of work gets,
which is the better reason. Tokens spent before the claim — the exploration
that decided which task to pick up — stay unattributed by design.

`cd` into the worktree the claim printed. Everything below happens there.

**Step 2: read the spec, not the plan of it.** The task's `about_doc` names the
accepted spec. `lode show <ref>` renders it, `-s <anchor>` one section;
`lode doc get <ref> --json` gives the same body plus the parsed section
anchors and edges. The backbone is the only copy — there is no corpus on disk.

**Step 3: write the plans.** Load `lode:splitting-specs-into-plans` if the spec is
large enough to need a numbered series, and `superpowers:writing-plans` for
each plan document. A plan's `covers:` frontmatter must name the spec sections
it undertakes, anchor by anchor: a whole-document edge discharges nothing, so
`lode doc list --needs-planning` would keep reporting the spec as unplanned.

Draft each plan in a scratch file, lint it, then create the document — the
backbone is where the plan lives, the scratch file is just the editor buffer:

```bash
lode doc anchors <path>                      # local lint: anchors and ## Tasks
lode doc new --kind plan --slug <slug> --file <path>
```

**Step 4: accept the plans.** `lode doc accept <id>` mints each plan's task set
in the accepting transaction (025 §9.2), which is what turns a written plan
into claimable work. Only the document's owner may accept it.

**Step 5: finish the design task.** `lode task set state merged <design-task-id>` once the
plan documents are accepted. Writing the plan is the deliverable; executing it
is the task set the acceptance just minted, not this task.
