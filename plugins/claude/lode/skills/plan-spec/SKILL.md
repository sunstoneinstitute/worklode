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

**Step 2: read the spec and its rules.** The task's `about_doc` names the
accepted spec. Read it with `lode show <ref> --inline`, then
`lode rule list --doc <ref> --json` for the rules it arranges. Read a rule with
`lode show <rule-ref>` to inspect its text, version, relationships and existing
governed tasks. Use `lode doc show <ref> --json` to map rules back to section
anchors for `covers`. The backbone is the copy of record.

**Step 3: write the plans.** Load `lode:splitting-specs-into-plans` if the spec is
large enough to need a numbered series, and `superpowers:writing-plans` for
each plan document. Split by the rules the work must satisfy, keeping standing
constraints with every part they govern. A plan's `covers:` frontmatter still
names the spec sections it undertakes, anchor by anchor: a whole-document edge discharges nothing, so
`lode doc list --needs-planning` would keep reporting the spec as unplanned.
A plan is also bounded by a server-enforced token budget (12 S19); `lode doc
add` warns past the soft budget and refuses past the hard ceiling, which is
the signal to split further.

Draft each plan in a scratch file, lint it, then create the document — the
backbone is where the plan lives, the scratch file is just the editor buffer:

```bash
lode doc lint <path>                          # local lint: anchors and ## Tasks
lode doc add --kind plan --slug <slug> --file <path>
```

**Step 4: accept the plans.** `lode doc accept <id>` mints each plan's task set
in the accepting transaction (025 §9.2), which is what turns a written plan
into claimable work. Only the document's owner may accept it. Acceptance also
links each minted task to the rules governing the plan. Check `lode rule list --doc <plan-ref>`
and a minted task's `governed_by` with `lode show <task-id> --json`.
Unresolved coverage refs select no rules; repair them before accepting.

**Step 5: finish the design task.** `lode task set state merged <design-task-id>` once the
plan documents are accepted. Writing the plan is the deliverable; executing it
is the task set the acceptance just minted, not this task.
