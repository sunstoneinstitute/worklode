---
name: worklode
description: Use when asked to explain Worklode itself — what it is, what entities it tracks (task, doc, project, deliverable, actor, approval), how task state changes automatically from GitHub/Flux webhooks vs. by hand, how to create or find a task or a spec/ADR/plan, what edges/relationships exist between objects, or how spec sections version (amend/supersede/anchors). Also use for "what lode commands exist", "how do I file a bug/spec here", or when a project's CLAUDE.md points to this skill. Not for the in-worktree done/block/release judgment loop (working-under-worklode) or credential handling (lode-secrets).
---

# Worklode

Sunstone's org-wide work tracker: one Go binary, `lode`, that is server, CLI
client, Kubernetes pod watcher, and migrator, backed by Postgres with an
append-only event log. This skill is the mental model — the entity/edge
catalog, the task state machine, the document model, and every `lode`
command in outline. Deep detail lives one hop away in `references/`; load
those on demand, not up front.

## Entities

| Entity | Id | For |
|---|---|---|
| Task | `WL-217` | Claimable work |
| Doc | numbered (spec/adr) or slugged (plan) | Specs, ADRs, plans, authored in the backbone |
| Project | slug | Umbrella over 1..n repos |
| Deliverable | `WL-DEL-3` | A shipped thing — state derived from reported facts, never a status a human sets |
| Actor | free text | Human, agent, or service account |
| Lease | numeric | One worktree's claim on one task |
| Approval | `(entity, id, revision)` | A required sign-off (currently: GitHub PR reviews only) |
| Issue / PullRequest / Artifact / Deployment | `(repo,n)` / numeric | GitHub and delivery facts, correlated to tasks |
| Event | monotonic id | The append-only log everything else derives from |

Full grammar, plus soft-delete semantics: `references/entities-and-edges.md`.

## Task lifecycle

```
draft → ready → in_progress → in_review → merged → deployed_dev → deployed_prod
                    ↑              ↓                                    ↓
                    └──────────────┘                            released
```

Every pre-merged state can go to `abandoned`; every terminal-ish state
(`merged`, `deployed_dev`, `deployed_prod`, `released`, `abandoned`) can be
sent back to `ready` by `reopen`, which requires a fresh claim.

**You drive:** `draft→ready` (`task ready`), `ready→in_progress` (`task
claim`/`next`), `in_progress→ready` (`task stop`/`release`),
`in_review→in_progress` (`task rework`), anything `→abandoned`
(`task abandon`), anything terminal-ish `→ready` (`task reopen`).

**Webhooks drive:** `in_progress→in_review` (a GitHub PR opens against the
task), `→merged` (the merge SHA lands on the default branch via a `push`
event — not the merge itself), `merged→deployed_dev/deployed_prod/released`
(Flux reconciliation, resolved forward-only against whatever facts have
arrived, in any order). Full event-by-event table: `references/webhooks.md`.

A task with `child_of` children can't itself sit in `in_review` or any
delivery state — `task done` on a parent reports the roll-up rule instead.

## Edges

| Between | Types |
|---|---|
| task ↔ task | `child_of` (subtask), `blocks`, `follow_up_to` (spun out of), `duplicate_of` (same request, filed twice) |
| task → doc | `plan_doc` (the plan that minted this task), `about_doc` (the doc a review/design task concerns) |
| doc ↔ doc | `covers` (plan→section, `full`\|`partial`\|`none`), `implements` (code→section), `amends`/`amendedBy`, `replaces`/`isReplacedBy`, `requires`, `wasDerivedFrom`, `blocks` (whole-plan ordering) |

Full table with direction and set-by command: `references/entities-and-edges.md`.

## Creating and viewing

```bash
lode task add --title "..." --kind bug --priority high   # kind: feature, bug, chore, design, review, spike
lode task list                          # open tasks; --status for delivered/abandoned too
lode task show <id>                     # body, edges, blocked status, lease holder
lode task claim [<id>]                  # lease it, create its worktree
lode next                               # claim the top-ranked ready task
lode task done
lode task block --by <id>
lode task abandon
lode board                              # in-progress / in-review / blocked / ready, at a glance
lode show <ref>                         # any entity by id: task, doc, project
lode timeline <id>                      # full history: states, PRs, CI, deploys

lode doc new --kind spec --slug <slug> --file <draft.md>   # kind: spec, adr, plan
lode doc list --needs-planning     # accepted specs with a section no accepted plan covers
lode doc list --needs-execution    # accepted plans whose minted task set still has an open task
lode doc get <id-or-slug> --json        # body, sections, edges
lode doc todo <slug> --deps             # one spec's remaining work, recursively
lode doc submit <id>
lode doc accept <id>
```

Every command also takes `--json`. Full command-by-command reference,
regenerated from the CLI itself so it can't drift: `references/commands.md`.

## Docs, briefly

Three kinds — `spec`, `adr`, `plan` — each `draft → accepted → superseded`.
A spec/ADR's `{#sec-N}` section anchors are frozen once accepted: amend or
supersede a section, never renumber it. **There is no consolidated "current
text" view** — what a section says now is that section plus whatever amends
it; `lode doc get <ref> --json`'s `edges_in` names `amendedBy`/`isReplacedBy`,
and following those is how you find out. Frontmatter is mandatory, always;
a plan's `covers` is how coverage becomes a query (`--needs-planning`) rather
than a status someone remembers to flip. Full frontmatter schema, the
cross-project `WL-SPEC-<n>` shorthand, and the doc-lifecycle watcher's two
minting rules: `references/specs-and-docs.md`.

Writing/revising a doc is itself an ordinary task (`kind: design`) that
closes on submission for review, not on acceptance. A plan's execution *is*
the task set minted when it's accepted — there's no container row above
them; "this plan's tasks" is a query, never something you create.

## Adding this to a project

Paste this into the project's `CLAUDE.md` so every session picks it up:

```markdown
## Work tracking

This project is tracked in Worklode. Work is claimed, not assigned — load
the `worklode` skill before filing or finding a task, and before creating or
reading a spec, ADR, or plan.
```

## Reference index

| File | Load it for |
|---|---|
| `references/commands.md` | Every `lode` command and its flags, generated from the CLI |
| `references/entities-and-edges.md` | Full entity/edge grammar, exact state-machine transitions, soft-delete |
| `references/specs-and-docs.md` | Frontmatter schema, amend/supersede mechanics, `WL-SPEC-<n>` shorthand, coverage queries |
| `references/webhooks.md` | Which GitHub/Flux event does what, event-by-event |

Neighbours: **working-under-worklode** owns the in-worktree done/block/release
loop once you've claimed a task; **lode-secrets** owns task-declared
credentials.
