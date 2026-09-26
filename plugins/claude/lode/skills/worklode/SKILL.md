---
name: worklode
description: Use when asked to explain Worklode itself — what it is, what entities it tracks (task, rule, doc, project, deliverable, actor, approval), how task state changes automatically from GitHub/Flux webhooks vs. by hand, how to create or find a task or a spec/ADR/plan, what edges/relationships exist between objects, or how specs arrange rules, rules version, and tasks inherit governing rules. Also use for "what lode commands exist", "how do I file a bug/spec here", or when a project's CLAUDE.md points to this skill. Not for the in-worktree done/block/release judgment loop (working-under-worklode) or credential handling (lode-secrets).
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
| Rule | `WL-RULE-12` | A design requirement with its own identity, status and version history |
| Doc | `WL-SPEC-25`, `WL-PLAN-7`, or slug | A spec arranges rules for reading; a plan is governed by the rules its work undertakes |
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

**You drive:** `draft→ready` (`task publish`), `ready→in_progress` (`task
claim`/`next` when the work needs a worktree; `task start` when it doesn't —
e.g. a design/review task that's just a judgment call, no code),
`in_progress→ready` (`task stop`/`release`), `in_review→in_progress` (`task
rework`), anything `→abandoned` (`task abandon`), anything terminal-ish
`→ready` (`task reopen`).

**Webhooks drive:** `in_progress→in_review` (a GitHub PR opens against the
task), `→merged` (the merge SHA lands on the default branch via a `push`
event — not the merge itself), `merged→deployed_dev/deployed_prod/released`
(Flux reconciliation, resolved forward-only against whatever facts have
arrived, in any order). Full event-by-event table: `references/webhooks.md`.

**No PR, no webhook — close it by hand:** a design or review task (settling
a decision, signing off on a proposal) has no code and no PR to drive the
rest of the lifecycle. Once the call is made, drive it yourself: `task
start` (ready→in_progress) → `task submit` (→in_review) → `task set state
merged` (→merged). `task start` refuses with a 422 if the task is already
assigned to someone else's identity ("assigned to X; unassign first") — run
`task unassign` first, then `start`.

A task with `child_of` children can't itself sit in `in_review` or any
delivery state — `task set state merged` on a parent reports the roll-up rule
instead.

## Edges

| Between | Types |
|---|---|
| task ↔ task | `child_of` (subtask), `blocks`, `follow_up_to` (spun out of), `duplicate_of` (same request, filed twice) |
| task → rule | `governedBy` (follows the newest version unless pinned) |
| spec → rule | Arrangement: rule, version, position, depth and section anchor |
| rule → rule | `refines`, `constrains`, `conflictsWith`, `references`, `wasDerivedFrom`, `amends`, `supersedes` (new rule → old rule; `amendedBy`/`supersededBy` are read from the far end, never stored) |
| plan → rule | `covers` (`full`\|`partial`\|`none`), resolved from the plan's document/section references at write time |
| task → doc | `plan_doc` (the plan that minted this task), `about_doc` (the doc a review/design task concerns) |
| doc ↔ doc | `implements` (code→section), `requires`, `wasDerivedFrom`, `blockedBy` (whole-plan ordering) |

Full table with direction and set-by command: `references/entities-and-edges.md`.

## Rally

A `rally` is a task kind that carries no work of its own. Its `blocks` edges
name the tasks a person picked as the thing to finish now. Those tasks, plus
everything they transitively wait on, sort first in `lode work next` — ahead
of critical and priority, in both default and `--strict-focus` mode.

One rally is active per project. A draft rally is inert and there may be any
number of them; `lode task publish` is what activates one, and publishing a
second while one is active is refused.

```bash
lode task add --title "Ship the cockpit" --kind rally --draft
lode task block <rally> --by <task>     # name a member: that task blocks the rally
lode task publish <rally>               # activate it
lode project rally <project>            # its members, and which are still open
```

A rally is never handed out as work: `lode work next` skips it and `lode task
claim` refuses it by id. It also takes no `child_of` children, blocks nothing,
and carries no decision — the edges pointing at it are its whole content. No
plan mints one; a rally is assembled by hand.

## Decisions

Any task but a rally can pose questions that someone has to answer. A
`decision` task is the case where the questions are the whole task: it is
never handed out as work (`lode work next` skips it, `lode task claim`
refuses it by id), it moves by `lode task assign`, and answering its last
open row closes it to `merged` in the same write.

```bash
lode decision add <task> --key storage --question "Where does the index live?" \
    --type single_select --option Postgres --option S3
lode decision list <task>               # the questions it poses, in authored order
lode decision show <task>/<key>         # one question, its options, its answer
lode decision edit <task>/<key>         # reword, regroup or re-parent — unanswered rows only
lode decision resolve <task>/<key> --pick Postgres   # record the answer
```

Recording is terminal: an answered row is never written over or edited. To
change a call, pose another row.

## Creating and viewing

```bash
lode task add --title "..." --kind bug --priority high   # kind: feature, bug, chore, design, review, spike, decision, rally
lode task list                          # open tasks; --status for delivered/abandoned too
lode task show <id>                     # body, edges, blocked status, lease holder
lode task claim [<id>]                  # lease it, create its worktree
lode work next                      # claim the top-ranked ready task
lode task start [<id>]                  # ready->in_progress, no worktree/lease (e.g. a design/review call)
lode task submit [<id>]                 # in_progress->in_review
lode task set state merged <id>         # in_review->merged
lode task unassign [<id>]               # clear assignee (needed before `start` on someone else's task)
lode task block --by <id>
lode task abandon
lode board                              # in-progress / in-review / blocked / ready, at a glance
lode project rally <project>            # the active rally and its open members
lode show <ref>                         # any entity by id: task, doc, project
lode search <query>                     # rank docs, tasks and skills by meaning and by exact token
lode task timeline <id>                 # full history: states, PRs, CI, deploys

lode doc add --kind spec --slug <slug> --file <draft.md>   # kind: spec, adr, plan
lode doc list --needs-planning     # accepted specs with a section no accepted plan covers
lode doc list --needs-execution    # accepted plans whose minted task set still has an open task
lode doc show <ref> --json        # body, sections, edges
lode doc todo <slug> --deps             # one spec's remaining work, recursively
lode doc progress                       # every spec in the project: how much exists, what moves it next
lode doc submit <id>
lode doc accept <id>
lode doc transfer <ref> --to <actor>    # owner-gated; move ownership to another actor
```

Every command also takes `--json`. Full command-by-command reference,
regenerated from the CLI itself so it can't drift: `references/commands.md`.

## Docs, briefly

A **spec is an arrangement of rules**. A rule has a stable ref such as
`WL-RULE-12`, its own text, status and version history. The arrangement puts
that rule at a position, depth and anchor in the spec. A section reference
names its place in a document; the rule ref names the requirement itself.
Accepting a spec accepts the draft rule versions it arranges.

A **plan is governed by the rules reached by its `covers` entries**. Its task prose
mints no rules. Acceptance mints tasks governed by those rules. Governance
follows the newest rule text unless pinned; reorganising a document does not
complete or rewrite those tasks. Coverage remains a query over plans and work.

Read the arrangement with `lode rule list --doc <spec-ref>` and a rule with
`lode show <rule-ref>`. Read a spec with `lode show <ref> --inline` to include
in-force document amendments and supersessions. A bare read returns its
current stored body; `--version` selects a historical version. Accepted
section anchors remain stable, and `covers` still uses document/section refs
and coverage levels. Use `spec` for new design documents; existing ADRs stay
readable. Frontmatter, rule editing, governance, and refactor mechanics:
`references/specs-and-docs.md`.

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
reading a spec, rule, or plan.

Specs arrange rules. Plans and their tasks are governed by rules. These live in the
Worklode backbone. Read a spec with `lode show <ref> --inline`; create a
document with `lode doc add`. When a
general-purpose planning skill says to save a design doc or plan under
`docs/` (`superpowers:brainstorming` and `superpowers:writing-plans` both
do), that path is a scratch buffer — the document is the `lode doc` row, and
nothing reads the file once the row exists.
```

## Reference index

| File | Load it for |
|---|---|
| `references/commands.md` | Every `lode` command and its flags, generated from the CLI |
| `references/entities-and-edges.md` | Full entity/edge grammar, exact state-machine transitions, soft-delete |
| `references/specs-and-docs.md` | Rule arrangements, versions, governance, frontmatter and coverage queries |
| `references/webhooks.md` | Which GitHub/Flux event does what, event-by-event |

Neighbours: **working-under-worklode** owns the in-worktree done/block/release
loop once you've claimed a task; **lode-secrets** owns task-declared
credentials.
