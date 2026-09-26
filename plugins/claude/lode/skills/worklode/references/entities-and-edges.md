# Entities, edges, and the task state machine

Deep reference for the `worklode` skill. Load this when a question is about
*what a fact means or how it connects to another fact*, not about which
command to run (that's `commands.md`) or how documents version (that's
`specs-and-docs.md`).

## Entity catalog

| Entity | Id / key | What it is |
|---|---|---|
| Project | slug (`worklode`) | An umbrella over 1..n repos (`project_repos`). Unbounded — there is no "milestone" class; a project is inherently ongoing. |
| Task | `<PROJECT_KEY>-<n>` (`WL-217`) | The unit of claimable work. Global sequence per project. |
| Rule | `<PROJECT_KEY>-RULE-<n>` | An independently versioned design requirement, arranged into specs and governing plans and tasks. |
| Doc | numbered per (project, kind), also addressable by slug | A spec arranges rules for reading; a plan is governed by the rules its work undertakes. |
| Deliverable | `<PROJECT_KEY>-DEL-<n>` | A thing the project ships (a service, a package) — never claimed or worked; state is derived from reported facts, not a status a human sets. |
| Actor | free text id | A human, agent, or service account. Carries `admin`, and since spec 029 the Keycloak identity claims (`groups`, `email`) recorded at login. |
| Lease | numeric id | One worktree's claim on one task. At most one active lease per task and per worktree. Ending a lease (release/done/block/abandon/reopen) never itself changes task state. |
| Approval | `(entity_kind, entity_id, subject_revision)` | Spec 029 §7.1's one-table model of "does this need a sign-off". Currently populated only from GitHub PR review requests (`awaiting → approved/rejected/changes_requested`); no general CLI verb for it yet. |
| Project participant | `(project_id, actor_id, role)` | Spec 029 §6.1 "Project Crew" — role-labelled, visible before any task is claimed. At most one `is_lead` and at most one `is_deputy` row per project. `lode project crew`. |
| Issue / PullRequest | `(repo, number)` | GitHub facts ingested by the App webhook, optionally correlated to a task. |
| Artifact / Deployment | numeric id | A built thing (`docker_image`, `pypi`, `git_tag`, `binary`) and where it landed (Flux Kustomization, PyPI, manual). |
| RuntimeEvent | numeric id | Pod-watcher facts: `crashloop`, `oom`, `flux_failure`, `flux_recovery`. |
| Blob | content digest | Content-addressed file storage; attached to tasks via `lode task attach`. |
| Inbox item | GitHub issue reference | A triage row (`new`/`promoted`/`dismissed`) — `lode inbox` turns issues into tasks without hand-copying. |
| Event | monotonic id | The append-only log everything above is derived from. Never deleted or compacted. `lode event tail --follow`. |

Tasks and docs are **soft-deleted**, never hard-deleted: `deleted_at`/`deleted_by`/`delete_justification` columns, `lode task delete`/`lode doc delete --justification`. Every event, edge, and artifact referencing a deleted row stays valid — `deleted_at IS NULL` is a filter, not a rewrite of history.

## Task state machine

```
draft → ready → in_progress → in_review → merged → deployed_dev → deployed_prod
                    ↑              ↓                      ↓             ↓
                    └──────────────┘                   released ←───────┘
```

Every pre-merged state can also go to `abandoned`; `merged`/`deployed_dev`/
`deployed_prod`/`released`/`abandoned` can all be sent back to `ready` by
`reopen` (which requires a fresh claim — the old lease does not resurrect).

**Who drives which arrow:**

| Transition | Driven by |
|---|---|
| `draft → ready` | You: `lode task publish` |
| `ready → in_progress` | You: `lode task claim` / `lode work next` |
| `in_progress → ready` | You: `lode task stop` / `lode task release` |
| `in_progress → in_review` | **Webhook.** A GitHub `pull_request` `opened`/`ready_for_review` event on an `in_progress` task. Also reachable by hand via rework's inverse. |
| `in_review → in_progress` | You: `lode task rework` (changes requested) |
| `→ merged` | **Webhook.** `pull_request` `closed` with `merged: true` records the merge SHA; a later `push` event to the repo's default branch is what actually calls `ResolveDelivery`, forward-only, in commit-arrival order — not the merge event itself. |
| `merged → deployed_dev` / `deployed_prod` / `released` | **Webhook.** Flux `Kustomization` reconciliation events, resolved against the repo's configured `done_state` (`project_repos.done_state`; falls back to a package default). Deployment facts can arrive out of order — `ResolveDelivery` always advances to the *furthest* milestone the recorded facts support, never backslides. |
| `deployed_dev → deployed_prod` / `released` | Webhook, same resolver. |
| `→ abandoned` | You: `lode task abandon` |
| terminal-ish → `ready` | You: `lode task reopen` (fresh claim required) |

A **container task** — one with children — cannot itself sit in
`in_review`/`deployed_dev`/`deployed_prod`/`released`; `lode task set state
merged` on a parent reports the roll-up rule instead of a bad transition.

Leases are orthogonal to state: a lease says a worktree is occupied, and
nothing in the delivery pipeline (merge, deploy, release) touches it. Leases
end only via release, done, block, abandon, reopen, or the expiry sweep.

## Edges

**Task ↔ task** (`task_edges`, `(from_task, to_task, type)`):

| Type | Meaning | Set by |
|---|---|---|
| `child_of` | Decomposition — `from_task` is a subtask of `to_task` | `lode task parent` / `lode task decompose` |
| `blocks` | `from_task` blocks `to_task` from proceeding. Edges into a `rally` task are its membership: they name what to finish now, and those tasks rank first in pickup. A rally is never a `from_task`. | `lode task block` |
| `follow_up_to` | `from_task` was spun out of the work on `to_task` | `lode task follow-up`, or `--follow-up-to` on `task add` |
| `duplicate_of` | `from_task` is the same request as `to_task`, which is the canonical one. A pointer only: it closes nothing, gates nothing, and moves nothing onto the canonical task | `lode task duplicate` |

**Rule relationships:**

| From → to | Meaning |
|---|---|
| Spec → rule | An arrangement records a rule version, position, depth and section anchor. |
| Plan → rule (`covers`) | The rules governing the plan. Each `covers` entry (a rule ref, a `<doc>#sec-N` ref, or a whole-document ref) is resolved to rules when the plan is written and stored as a `covers` edge from the plan to each rule, carrying its `full`\|`partial`\|`none` coverage level. |
| Task → rule (`governedBy`) | Governing requirement, linked by plan acceptance, by hand, or by the `Spec:` trailer gate. The link records the version at link time and follows the newest text unless pinned. |
| Rule → rule (`refines`, `constrains`, `conflictsWith`, `references`) | Design relationships. Text references also produce derived `references` edges. |
| Rule → rule (`amends`) | The subject changes how the object is read without replacing it; `lode show --inline` folds it in. Documents do not amend or replace each other. |
| Rule → rule (`wasDerivedFrom`) | Lineage recorded after a split. |
| Rule → rule (`supersedes`) | Runs from the new rule to the old rule it replaces, written only by a refactor (`lode rule supersede`). The old rule is withdrawn; its task links remain and resolve to live successors. `supersededBy` is the same fact read from the old rule's end, never stored. |

Read a rule's `arranged_in`, `governed_tasks` and `edges` with
`lode show <rule-ref> --json`. A task's `governed_by` shows its governing
rules, link-time and current versions, pins, and `resolves_to` successors.
Commands and editing constraints: `specs-and-docs.md`.

**Task → doc:**

| Column | Meaning |
|---|---|
| `tasks.plan_doc` | The plan whose acceptance minted this task (025 §9.2). Nullable — a task no plan authored carries none. |
| `tasks.about_doc` | The document a review or design task is *about* — set on review tasks minted at submission and design tasks minted at acceptance (025 §15.4). |

Nothing yet records the reverse — which task *authored* a new document
(`prov:wasGeneratedBy`, 025 §12) is asserted in `ns/concept.ttl` but has no
column or write path (WL-217). Don't assume it's queryable.

**Doc ↔ doc** (`doc_edges`, one row per directed edge, `from_doc`/`from_anchor`
→ `to_doc`/`to_anchor` or `to_external` for a reference this backbone can't
resolve):

| Type | Direction / meaning |
|---|---|
| `implements` | Component (code) → doc section — "this code realises this intent". Retired spelling: `covers` used to mean this too; `implements` is now the only term for it. |
| `requires` | Dependency between docs. |
| `wasDerivedFrom` | Provenance: this doc grew out of that one. |
| `blockedBy` | Orders whole plan *documents* (never section-scoped): stored from the later plan to the earlier one, read back from the earlier plan as `blocks`. |

`covers` is a row in the same `doc_edges` table, but points at a rule
(`to_rule`) rather than a document: see plan → rule in the rule-relationships
table above. A `partial` may name the other plans that jointly close the
coverage (`fullCoverageWith`, in `doc_coverage_completed_with`).

`lode doc list --needs-planning` / `--needs-execution` are standing queries
over the edges above, not stored flags. `--bare-superseded` reads rule edges
instead — withdrawn rules with no `supersedes` edge pointing at them — see
`specs-and-docs.md`.

## Webhooks in one paragraph

GitHub App and Flux webhooks are HMAC-signed and land in `internal/hooks`;
`lode inbox import` replays the same store path for backfill, so re-running is
always safe. They only ever *record facts* (a PR opened, a check ran, a
Kustomization reconciled) and then call the one place lifecycle rules live —
`Transition` for the single `in_progress→in_review` jump, `ResolveDelivery`
for everything from `merged` onward. See `webhooks.md` for the event-by-event
table.
