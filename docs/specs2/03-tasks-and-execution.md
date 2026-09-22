# Tasks and execution

The execution backbone is the Postgres core that Worklode's pickup loop runs on. It owns projects and their task keys, tasks and their state machine, the four edge types between tasks, worktree-bound leases, the atomic claim transaction, the append-only event log that every mutation writes through, and the webhook-driven delivery facts that move a task from merged to deployed or released. On top of that core sit the ranking that decides what `lode task claim --next` picks, the tombstone that hides a row that should never have existed, and the research-work entities (milestones, deliverables, crew, approvals, intake) that let journalists and data scientists work the Sunstone Way in the same backbone. Everything below is described as it stands today.

## 1. Projects and task keys

A project is the scoping unit for tasks, focus, repos and (for research work) milestones and crew. Every project has a **key**: required at creation, unique, uppercase, immutable, matching `^[A-Z][A-Z0-9]{1,9}$`. `SPEC` and `ADR` are rejected as keys because they are the type token in the `<KEY>-<TYPE>-<n>` document shorthand (see 05-documents.md). The key is immutable because it is baked into task ids, branch names and `Worklode-Task:` trailers.

Task ids are `<KEY>-<n>` (`WL-12`, `SW-1`). Each project carries `next_task_num`, and `CreateTask` allocates the id inside its own transaction:

```sql
UPDATE projects SET next_task_num = next_task_num + 1
WHERE id = $1 RETURNING key, next_task_num - 1
```

There is no global counter. Every parser that recognises a task id matches `[A-Z][A-Z0-9]*-\d+`. `numericTaskID` parses the digits after the last `-`.

Branch names are rendered by the server from `LODE_BRANCH_TEMPLATE` (default `{{ .id }}-{{ .slug }}`), and `store.TaskIDFromRef` derives its pattern from that template. Worktrees live at `<worktree_dir>/<branch>` (default `.worktrees/<id>-<slug>`). The template grammar and worktree naming are defined in 08-agent-harness-and-sessions.md.

`lode install` binds a `commit-msg` git hook that stamps `Worklode-Task: <id>` into any commit made inside a task worktree, resolving the id from the worktree's git config with no backbone call. The trailer is the only correlation signal that survives rebase, cherry-pick and squash. The hook stamps only a message that already has a body, skips while `MERGE_HEAD` is set, and leaves an existing trailer alone.

Projects also carry, for research work (§13): `seeded_by` (nullable reference to the intake task the project was promoted from), free-form key/value **labels** (`kind=sunstone-story` is the first with meaning), `horizon` (`bounded` or `standing`), `approval_flow` (a snapshot of the named, versioned review flow), and `chat_space_name` (nullable Google Chat space resource name). `horizon` is an attribute, so a standing infrastructure project is still a project with a key, tasks and a focus.

Surface: `store.CreateProject(ctx, id, name, key)`; `POST /api/v1/projects` with `key` required, a unique violation answered as 400; `lode project add <id> --name … --key WL`; `lode project list` shows a `KEY` column.

## 2. Tasks

### 2.1 Row

```sql
CREATE TABLE tasks (
    id         text PRIMARY KEY,                    -- <KEY>-<n>
    project_id text NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    title      text NOT NULL,
    body       text,
    priority   text NOT NULL CHECK (priority IN ('critical','high','medium','low')),
    kind       text NOT NULL CHECK (kind IN
                 ('feature','bug','chore','design','review','spike','decision','rally')),
    state      text NOT NULL CHECK (state IN
                  ('draft','ready','in_progress','in_review','merged',
                   'deployed_dev','deployed_prod','released','abandoned')),
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
```

Further columns: `concern` (nullable, §8.1), the `needs-decomposition` label (§8.6), `milestone_id` (nullable, §13.2), `about_doc` (nullable, the document a review or design task is about, §9.6), `assignee` (one, nullable, §13.5), and the three tombstone columns of §12. `plan_doc` says which plan minted a task and is a different fact from `about_doc`. Conventions everywhere: `timestamptz`, `bigint GENERATED ALWAYS AS IDENTITY` keys, `boolean` flags, `jsonb` payloads, partial unique indexes and `CHECK` constraints.

### 2.2 Kinds

The kind CHECK, the Go `validKinds` list and `wlc:TaskKind` in `ns/` change together, and a test fails when they disagree.

| Kind | Meaning |
|---|---|
| `feature`, `bug`, `chore` | Ordinary claimable, worktree-bound work that lands a diff. `reconcile` and similar activities are `chore`. |
| `design` | Author or revise a Worklode document (spec or plan). Closes when the document is submitted for review. The document is reachable via `prov:wasGeneratedBy`. |
| `review` | Review a submitted document. Minted by the `doc-lifecycle` subscriber (§9.6). |
| `spike` | Time-boxed experiment, throwaway by convention. |
| `decision` | Answer the questions posed on the task and record the answers (§11). Never claimed into a worktree. Worked through the lease-free assign/start/submit path. |
| `rally` | A hand-assembled goal whose only content is the `blocks` edges pointing at it (§8.3). Never claimed, takes no children, blocks nothing, carries no decision. |

No kind is a container. Being a container is inferred from having `child_of` children (§5). `rally` is the one declared structural role, because a rally is assembled one edge at a time and has no creating act that could make its edges the test.

### 2.3 Priority and concern

`priority` is `critical | high | medium | low` and says how urgent. `concern` says what kind of value the task delivers, from a closed enum: `completeness`, `performance`, `usability`, `security`. A task has at most one concern. The two are independent. Both enums grow only by schema change.

## 3. Task state machine and delivery

### 3.1 States

| State | Meaning |
|---|---|
| `draft` | Not claimable. |
| `ready` | Claimable. |
| `in_progress` | Leased into a worktree (or started through the lease-free path). |
| `in_review` | Submitted for review. |
| `merged` | Work landed on the default branch, auto-detected or set by hand with `lode task set state merged`. For a task with children: all children delivered. |
| `deployed_dev` | A dev/test deploy covers the landed commit: GitHub `deployment_status: success` and Flux `ReconciliationSucceeded`. |
| `deployed_prod` | Same, for prod. |
| `released` | A GitHub release covers the landed commit. |
| `abandoned` | Considered and dropped. Reachable only from states before `merged`. |

### 3.2 Transitions

The table below is the built-in `default` workflow. The authority on any task's legal transitions is the per-project workflow that governs it: the mandatory core edges hold everywhere, and entries into `in_review` and the delivery states exist only where the workflow declares them. Workflows are defined in 04-done-verification-workflows.md.

| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | publish (`lode task publish`) |
| `ready` | `in_progress` | claim (lease acquired), or start on the lease-free path |
| `in_progress` | `in_review` | submit for review |
| `in_progress` | `ready` | release, or lease expiry |
| `in_review` | `merged` | accept |
| `in_review` | `in_progress` | rework |
| `ready`, `in_progress`, `in_review` | `merged` | landing on main (resolver), or `lode task set state merged` |
| `merged` | `deployed_dev`, `deployed_prod`, `released` | delivery facts (§10) |
| `deployed_dev` | `deployed_prod`, `released` | delivery facts |
| `draft`, `ready`, `in_progress`, `in_review` | `abandoned` | `lode task abandon` |
| `merged`, `deployed_dev`, `deployed_prod`, `released`, `abandoned` | `ready` | `lode task reopen` |

`Transition(tx, now, taskID, from, to, eventID)` is the one guard. Inside the caller's transaction it verifies both that the move is in the transition set and that the task's current state equals `from`, bumps `updated_at`, appends a `state_log` row attributed to `eventID`, and finally resolves the parent's roll-up if the task has one (§5.4). Unknown task gives `ErrNotFound`, wrong from-state gives `ErrBadTransition`.

`lode task set state merged <id>` reads the current state and moves the task to `merged` from wherever it is, the way `abandon` does. It does not require `in_review`. `draft` and the terminal states refuse it. The same command accepts `deployed_dev`, `deployed_prod` and `released`, so a missed webhook can be reconciled by hand without widening the state machine. It is the right close for work with no webhook (an admin UI change, a CMS edit). On a task with children it is an error naming the roll-up rule (§5.2).

Delivery transitions are forward-only. The resolver never walks a task backward and never advances a `draft`. `deployed_prod` and `released` are peers: neither is ahead of the other, and there is no transition between them.

**Reopen** also clears the task's `task_commits` in the same transaction, so the next webhook cannot resolve the task straight back to the state it left. Delivery has to be re-earned by new work landing.

**Delivery does not close the lease.** The lease ends only on `release`, `abandon`, `reopen`, or the expiry sweep. Mutual exclusion is already enforced by `Claim` requiring `ready`, and liveness by the TTL sweep. Work legitimately continues in a worktree after its branch is deployed to dev.

### 3.3 `done_state` per repo

Each repo mapping carries a `done_state`: `merged`, `deployed_prod` or `released`. It is the state that counts as fully delivered for that repo, and it selects which delivery branch the resolver walks:

| `done_state` | Branch walked | Ignores |
|---|---|---|
| `released` | `merged → deployed_dev → released` | prod deploys |
| `deployed_prod` | `merged → deployed_dev → deployed_prod` | releases |
| `merged` | same as `deployed_prod` | releases |

A `done_state = merged` task still advances past `merged` when deploy facts exist, because `merged` is also the default for repos discovery has not profiled and a real signal outranks a default.

Discovery seeds `done_state` at `lode project repo add` through the GitHub App: prod environment gives `deployed_prod`, releases without a prod environment give `released`, neither gives `merged`. It is settable with `lode project repo edit <repo> --done-state`. Discovery never gates transitions. It runs only at `repo add`, so a repo that later gains a prod environment keeps its old value until set by hand. Without the App's **Actions: read** permission the environments call returns 403, discovery fails with a `discover repo done_state` warn log, and every repo keeps `merged`.

### 3.4 Closed

`taskClosed` is a predicate joined through the repo mapping. A task is closed when it is `abandoned`, or has reached its repo's `done_state` or a later state on that repo's delivery path. A task's repos are the ones its work landed in (`task_commits` joined with `main_commits`). A task landing in several repos is closed only when it satisfies the strictest. A task with children is closed at `merged` in every repo, because it can never advance past it.

## 4. Edges

```sql
CREATE TABLE task_edges (
    from_task  text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    to_task    text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    type       text NOT NULL CHECK (type IN
                 ('child_of','blocks','follow_up_to','duplicate_of')),
    created_at timestamptz NOT NULL,
    UNIQUE (from_task, to_task, type)
);
CREATE UNIQUE INDEX task_edges_single_parent    ON task_edges (from_task) WHERE type = 'child_of';
CREATE INDEX        task_edges_children         ON task_edges (to_task)   WHERE type = 'child_of';
-- single origin and single canonical: same shape as task_edges_single_parent
CREATE UNIQUE INDEX task_edges_single_origin    ON task_edges (from_task) WHERE type = 'follow_up_to';
CREATE UNIQUE INDEX task_edges_single_canonical ON task_edges (from_task) WHERE type = 'duplicate_of';
```

| Type | `A → B` means | Decides | Cardinality | Cross-project | Cycle check |
|---|---|---|---|---|---|
| `blocks` | B cannot be claimed while A is open (§3.4) | when a task may be claimed | many to many | yes | none needed |
| `child_of` | B is A's parent (§5) | what a task is made of | one parent per task | no | yes, `reachesViaChildOf` (BFS up the parent chain), `ErrCycle` |
| `follow_up_to` | A was spun out of work on B | nothing (provenance) | one origin per task | yes | none |
| `duplicate_of` | A and B are the same request, B is canonical | nothing (provenance) | one canonical per task | yes | none |

All types reject self-edges (`ErrInvalidInput`) and duplicates (`ErrEdgeExists`). `IsBlocked(tx, taskID)` runs inside the claim transaction. `follow_up_to` and `duplicate_of` gate nothing and grant no parenthood: a follow-up does not make its origin a container, and a duplicate stays claimable until closed by hand. Marking a duplicate changes nothing about the canonical task (no absorption of skills, edges, body or priority). A `blocks` edge may not start at a rally, and a `child_of` edge may not point into one.

Surface: `POST`/`DELETE /api/v1/tasks/{id}/edges` for every type. `POST /api/v1/tasks` accepts `parent` and `follow_up_to` so a child or follow-up is created with its edge in one transaction. There is no create-time `duplicate_of`. CLI:

| Command | Effect |
|---|---|
| `lode task block <id> --by <id>` / `unblock` | `blocks` edge |
| `lode task blockers <id>` | open blockers, transitively |
| `lode task add --parent <id>` / `--follow-up-to <id>` | create with edge |
| `lode task parent <id> --under <id>` / `unparent <id>` | `child_of` |
| `lode task follow-up <id> --of <id>` / `unfollow-up <id>` | `follow_up_to` |
| `lode task duplicate <id> --of <id>` / `unduplicate <id>` | `duplicate_of` |
| `lode task tree [<id>]`, `lode task list --parent <id>` | read hierarchy |

The task page lists Parent, Children, Follow-up to, Follow-ups, Duplicate of and Duplicates.

**Governing clauses.** A task carries direct links to the design clauses that govern it (05-documents.md §4, 12-spec-refactoring-design-tree.md S2 and S3), stored in `task_governed_by (task_id, clause_id, clause_version, source)`. Accepting a plan links every task it mints to each clause the plan's `covers` edges reach: a section-scoped edge reaches the clause at that anchor and the clauses arranged under it, a document-scoped edge reaches every clause the document arranges. The plan is never the source of truth for the link afterwards; a stale or withdrawn plan leaves its tasks governed. `clause_version` records the clause version current when the link was made, and the link resolves to the clause's newest version. A link may be pinned when it is made (`--pin`); a pinned link resolves to that version and reports the versioned page `/projects/<proj>/clause/<n>/<ver>`. Governing the same clause again without the pin unpins it. After minting, only an architect changes the links, by hand, and every change is an event on the task (`task.governed`, `task.ungoverned`). A task created without a plan may name its clauses at creation and may acquire them later. Those events live in the event log only; task timelines show state transitions and leave governance changes to the task detail and `lode event tail` (12-spec-refactoring-design-tree.md S34). Re-accepting a plan whose `covers` widened governs only the tasks that re-accept mints; earlier tasks keep their links until an architect adds to them (S31). A clause ref carries its project key, so a task may be governed by another project's clause (S32).

| Surface | Effect |
|---|---|
| `POST /api/v1/tasks` with `governed_by: [WL-CL-12]` | create with links |
| `POST` / `DELETE /api/v1/tasks/{id}/governed-by` with `{"clause": "WL-CL-12", "pin": true}` (`pin` is read only by `POST`; `DELETE` ignores it) | add or remove one link |
| `GET /api/v1/tasks/{id}` field `governed_by` | the links with each clause's current version |
| `lode task add --governed-by WL-CL-12` | create with a link, repeatable |
| `lode task govern <id> --by WL-CL-12` / `ungovern` | add or remove one link |
| `lode show <id>` | one `governed by` line per link, noting when the clause has a newer version |

## 5. Hierarchy and decomposition

A plan is a document. Accepting it mints its tasks directly with no root row above them (05-documents.md). `child_of` exists to decompose an oversized task. Every rule below applies to a task that has children, whatever its kind.

| Decision | Rule |
|---|---|
| Container identity | Inferred: has `child_of` children |
| Parents per task | Exactly one |
| Depth | Max 2 edges (task → subtask) |
| Parent claimable | Never: excluded from the ready set and rejected in `Claim` |
| Parent states | `in_review`, `deployed_dev`, `deployed_prod`, `released` rejected; only core states |
| Closure | Automatic roll-up, forward and backward |
| Progress | `closed_children / total_children` over direct children, derived on read |
| Cross-project children | Rejected (422) |
| Blocker inheritance | None; children do not inherit a parent's blockers |
| Child ordering | Out of scope |

### 5.1 Decomposition

```
lode task decompose <id> --into "Title A" "Title B" "Title C"
```

One transaction: clear `needs-decomposition`, create the N children inheriting project, priority and concern, wire the `child_of` edges, leave the children `draft`. The parent's kind is untouched. Rejected while the parent holds an active lease. The oversized task keeps its id and every reference to it, and its children become the claimable units. `POST /api/v1/tasks/{id}/decompose` is the endpoint.

### 5.2 State machine of a task with children

| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | manual publish |
| `ready` | `in_progress` | any child started or closed, including abandoned |
| `in_progress` | `merged` | every child closed, at least one delivered |
| `in_progress` | `abandoned` | every child abandoned, or manual abandon |
| `merged` | `ready` | a child reopens |

`ResolveDelivery` returns early for a task with children. A manual `set state merged` is refused.

### 5.3 Roll-up

`ResolveHierarchy(tx, now, parentID, eventID)` reads the children and applies the table. Zero children: nothing fires. All abandoned: `abandoned`. Mixed abandoned and delivered: `merged`. A child back to `ready` puts the parent back to `ready`.

### 5.4 Hooked into `Transition`

`Transition` ends by resolving the parent, if any, with the same `tx`, `now` and `eventID`, so the child's event attributes the parent's move and no call site can forget it. Recursion ends at depth 2.

### 5.5 API and brief

`GET /api/v1/tasks/{id}` carries `hierarchy: { parent: {id, title, state} | null, progress: {closed, total} }`. `POST …/edges` rejects a second parent (409), a cross-project edge (422) and a depth overflow (422). `store.Brief` carries `Parent` (id, title, state) exactly one hop up. `lode task show` prints `Parent:` and `Progress: 3/7`; `lode task board` groups children under their parent.

## 6. Leases and worktrees

```sql
CREATE TABLE leases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text NOT NULL REFERENCES tasks(id)  ON DELETE RESTRICT,
    actor_id    text NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
    worktree    text NOT NULL,
    acquired_at timestamptz NOT NULL,
    renewed_at  timestamptz,
    expires_at  timestamptz NOT NULL,
    released_at timestamptz
);
CREATE UNIQUE INDEX leases_active          ON leases (task_id)  WHERE released_at IS NULL;
CREATE UNIQUE INDEX leases_active_worktree ON leases (worktree) WHERE released_at IS NULL;
```

A lease is keyed by git-worktree identity. `worktree` is an opaque, stable string the agent harness supplies (recommended `<host>:<abs-worktree-root>`); the backbone never parses it. A task has at most one active lease and a worktree holds at most one task. Sessions come and go against the same worktree while the single lease persists. `DefaultLeaseTTL = 2h`.

| Step | Behaviour |
|---|---|
| Acquire | Through `Claim` (§7). `acquired_at = renewed_at = now`, `expires_at = now + ttl`. |
| Renew | `renew(taskID, actor)` sets `renewed_at` and `expires_at` on the active lease held by that actor. A non-holder gets `ErrNotFound`, indistinguishable from "no lease". An expired but unswept lease is still renewable. Renewal is a commit-cadence heartbeat: the pre-commit hook calls it before each commit batch. |
| Release | `release(taskID, actor)` sets `released_at`. A task still `in_progress` returns to `ready`; a task that moved on keeps its state. Non-holder gets `ErrNotFound`. Triggered by worktree removal: the lease dies with the worktree. |
| Expiry sweep | `ExpireLeases(now)` closes every active lease past `expires_at`, reverts each still-`in_progress` task to `ready`, and fires one `system` `lease.expired` event per lease, idempotent on `lease-expired-<leaseID>`. Each expiry re-checks in its own transaction. The server runs it on a ticker. |

Closing a lease by any path also stamps `ended_at` on every open `agent_sessions` row for it, in the same transaction, with no event of its own (08-agent-harness-and-sessions.md).

Surface: `POST /api/v1/tasks/{id}/claim` (`worktree`, `ttl`), `…/renew`, `…/release`; holder identity is `(actor, worktree)`. `lode task claim|renew|release --worktree <id>`; the hooks supply the value. The claim and claim-next responses carry a server-derived `branch`. `lode task reopen <id>` is the reopen path.

## 7. The claim transaction

```
Claim(ctx, taskID, actorID, worktree, ttl) (*Lease, error)
```

Executed as the `apply` callback of `RecordEvent("cli", <minted id>, "lease.claimed", …)`:

1. `SELECT … FROM tasks WHERE id = $1 FOR UPDATE`. Concurrent claims of the same task serialize here; claims of different tasks never contend. Unknown task gives `ErrNotFound`.
2. Verify the actor exists (`ErrNotFound`).
3. Verify no active lease (`ErrLeased`).
4. Verify not blocked: `IsBlocked` (`ErrBlocked`).
5. Verify claimable: not a task with children, not `decision` or `rally` kind, not labelled `needs-decomposition`, not deleted.
6. `Transition(tx, now, taskID, "ready", "in_progress", eventID)` (`ErrBadTransition`).
7. `INSERT INTO leases … RETURNING id`. The `leases_active` index is the backstop: a unique violation (`23505`) maps to `ErrLeased`.

All steps, the event row and the `state_log` row commit or roll back together. `Claim` is a total, atomic function of one candidate id. Ranking, candidate construction and retry policy live in §8; on a lost race `claim --next` re-ranks and tries the next candidate.

## 8. Prioritization and pickup

### 8.1 Focus

`project.focus` is an ordered list of concerns, the project's current steering. It is a soft filter: it directs the choice only when no higher signal decides, and it never removes rows. Agents never idle while ready work exists. `concern_rank(task)` is the task's index in `focus`; an unlisted or null concern takes the worst rank.

### 8.2 Ready set

`claim --next` selects from tasks that are `ready`, unblocked (no open blocker), unleased, live (not deleted) and claimable. `readyCandidates` excludes a task with children and `kind IN ('decision','rally')`:

```sql
AND NOT EXISTS (SELECT 1 FROM task_edges c WHERE c.to_task = t.id AND c.type = 'child_of')
AND t.kind NOT IN ('decision', 'rally')
```

and excludes the `needs-decomposition` label.

### 8.3 The rally

A rally is a `kind = 'rally'` task. Its content is the `blocks` edges pointing at it. It is how a person overrides the computed order without editing every task's priority.

```
lode task add --kind rally --draft --title "Ship the cockpit"
lode task block <rally-id> --by <task-id>      # once per member
lode task publish <rally-id>                   # draft -> ready activates it
lode project rally [<project>]                 # the active rally and its members
```

A draft rally is inert. A rally is active when its state is neither `draft` nor closed. **At most one rally is active per project**, enforced by a partial unique index on `tasks (project_id)` over rows where kind is `rally` and state is not `draft`, `merged`, `deployed_dev`, `deployed_prod`, `released` or `abandoned`. Retiring a rally means closing it. Membership is the transitive open-blocker closure of the active rally, the same relation `lode task blockers` walks. The rally arm is soft: when every member is closed or leased, agents drift to other work.

### 8.4 Ranking

Default sort key, descending signal:

```
(in_rally, is_critical, concern_rank, priority, blocking_fan_out)
```

| Arm | Meaning |
|---|---|
| `in_rally` | member of the active rally's closure; true first |
| `is_critical` | `priority == critical`; true first; critical bypasses focus |
| `concern_rank` | index in `project.focus`; lower first |
| `priority` | `critical > high > medium > low` |
| `blocking_fan_out` | how many tasks the task transitively unblocks over `blocks`; higher first; the estimate-free criticality proxy |

Ties resolve by oldest `created_at`, then task id, so ordering is reproducible and starvation-free. The server takes the top row and leases it in the same transaction.

### 8.5 `claim --next` and `--strict-focus`

```
lode task claim --next [--project <id>] [--strict-focus] [--dry-run] [--json]
```

| Flag | Effect |
|---|---|
| `--project <id>` | restrict to one project and its `focus`; omitted means every project the caller may work, each task ranked by its own project's focus |
| `--strict-focus` | drop `is_critical` from the key: `(in_rally, concern_rank, priority, blocking_fan_out)`; the rally arm survives |
| `--dry-run` | return the task that would be claimed without leasing it; never a pick-then-claim step |
| `--json` | `{ "claimed": true, "task": { id, slug, concern, priority, fan_out, project, lease: { worktree, expires_at } } }` |

None ready is exit 0 with `{ "claimed": false, "reason": "no-ready-task" }`; the 24/7 loop backs off. Errors exit non-zero with `{ "error": … }`. Two concurrent calls get different tasks, or one gets "none ready". There is no persistent per-project strict setting.

### 8.6 `needs-decomposition`

A task label meaning the task's projected context (brief, governing spec or plan excerpt, affected components, definition of done) exceeds the smart zone, a server-side configurable token budget with default about 100k. The call is made by a reviewer, human or agent, at review. A labelled task is not claimable at all until split with `lode task decompose` (§5.1). Deciding how to split is itself a normal claimable task.

## 9. Event log and provenance

### 9.1 Tables

```sql
CREATE TABLE events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source      text NOT NULL,           -- open set, see below
    external_id text NOT NULL,
    type        text NOT NULL,
    payload     jsonb,
    received_at timestamptz NOT NULL,
    txid        xid8 NOT NULL DEFAULT pg_current_xact_id(),
    UNIQUE (source, external_id)
);
CREATE INDEX events_txid_id ON events (txid, id);

CREATE TABLE state_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind text NOT NULL,
    entity_id   text NOT NULL,
    change      jsonb NOT NULL,
    event_id    bigint NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    at          timestamptz NOT NULL,
    txid        xid8 NOT NULL DEFAULT pg_current_xact_id()
);

CREATE TABLE event_subscribers (
    name              text PRIMARY KEY,
    last_read_offset  bigint NOT NULL DEFAULT 0,
    last_acked_offset bigint NOT NULL DEFAULT 0,
    updated_at        timestamptz NOT NULL,
    CHECK (last_acked_offset <= last_read_offset)
);
```

Events are never deleted or compacted. `events.source` is an open set with no CHECK: `github`, `flux`, `watcher`, `cli`, `web`, `system`, and one value per ingest system (CMS, CI publish, pipeline). `events.type` has no CHECK because webhook deliveries keep their vendor dotted types (`push`, `issues.opened`) in the same table as domain events; the domain-event set is enforced in Go at emit time.

### 9.2 `RecordEvent`

`RecordEvent(source, externalID, type, payload, apply)` is the sole write entry point. It inserts the event with `ON CONFLICT (source, external_id) DO NOTHING`; on first sight it calls `apply(tx, eventID)` in the same transaction; on a repeat it returns the existing id with `inserted=false` and skips `apply`. Every typed-table mutation (task transition, lease insert or close, edge change, fact-table write) is an `apply` callback, so the event and the change commit or roll back together, and every change carries provenance through `state_log.event_id`. An event that could commit without its change, or a change without its event, does not exist.

`external_id` is deterministic wherever possible: domain events use `<type>:<subject>:<version>`, sweeps use `lease-expired-<leaseID>`, subscriber actions use `<subscriber>:<rule>:<event-id>`. Events with no natural key mint a random hex id. A retried client request therefore gets the existing event rather than a duplicate.

The event's `@id` is `wlid:event/<id>`, so the id is reserved before the insert (`nextval` on the identity sequence, `OVERRIDING SYSTEM VALUE`). A minted task id is the exception: `task.created` and `issue.promoted` allocate it from the project counter inside `apply` and set `task` on the just-inserted row in the same transaction, which no reader can observe half-done because readers stay below the commit horizon.

### 9.3 Total order and the commit horizon

`events.id` is assigned at insert and visible at commit, so a reader tracking `last_seen_id` can skip a slow transaction's lower id forever. Subscribers therefore read only below the commit horizon:

```sql
SELECT id, source, type, payload, received_at FROM events
 WHERE id > $1 AND txid < pg_snapshot_xmin(pg_current_snapshot())
 ORDER BY id LIMIT $2;
```

A visible row below the horizon is committed for good, so the visible log grows only at its tail. Consequences: aborted transactions leave holes (offsets are positions in the sequence); writers are not serialized; any long transaction anywhere holds the horizon back for every subscriber, which the lag metric makes visible. `graph_projection.last_txid` uses the same horizon for the graph projector's watermark.

### 9.4 Subscribers

The model is a Kafka consumer group in one row. **Read** takes the batch after `last_read_offset` below the horizon and advances `last_read_offset`. **Ack** advances `last_acked_offset` forward only to the newest completely processed offset. **Restart** resumes at `last_acked_offset`; everything read but unacked is redelivered. Delivery is in order and at least once, so handlers must be idempotent.

One process holds a stream: the loop takes a dedicated pool connection and `pg_try_advisory_lock(hashtext('wl:subscriber:' || name))` for its lifetime. A second replica fails the lock, idles and retries. Release calls `pg_advisory_unlock` and then destroys the session rather than returning it to the pool; any lock-path error is treated as "not held". The loop polls (default 1s). A subscriber row is created by its owner at startup with `ON CONFLICT DO NOTHING`, so a new subscriber replays the whole log; `lode event seek` chooses otherwise. `GET /api/v1/events/stream` and `lode event tail --follow` serve the log live.

### 9.5 Event types and payloads

Domain events carry a curie in `type` and JSON-LD in `payload` (`@type: wl:DocumentAccepted`, `prov:atTime`, `prov:wasAssociatedWith`, `wl:subject`, per-type properties). `wl:Event rdfs:subClassOf prov:Activity`, one subclass per type, generated from `ns/` so vocabulary and code cannot drift (07-knowledge-graph-and-search.md). Every payload names its subject: an event about a task carries `task`, an event about a document carries `doc`.

| Event | Source | Payload |
|---|---|---|
| `task.created` | `cli`, `web`, `watcher` | creation input plus minted `task` |
| `task.updated` | `cli`, `watcher` | changed fields plus `task` |
| `task.skills_set`, `task.checklist_set` | `cli` | pinned skills / item id and `checked`, plus `task` |
| `task.decomposed` | `cli` | child titles plus `task` (the parent) |
| `task.assigned`, `task.unassigned`, `task.started`, `task.stopped` | `cli` | `task`, plus assignee or actor |
| `task.done`, `task.abandoned`, `task.reopened` | `cli` | `task` |
| `task.deleted`, `task.undeleted` | `cli` | `task`, `actor`, `justification` |
| `task.edge_added`, `task.edge_removed` | `cli` | `from`, `to`, `type` |
| `lease.claimed`, `lease.rebound` | `cli` | `task`, `actor`, `worktree` |
| `lease.renewed`, `lease.released` | `cli` | `task`, `actor` |
| `lease.expired` | `system` | `task`, `lease` |
| `agent_session.started`, `agent_session.ended` | `cli` | `task`, `actor`, `agent`, `session` |
| `secrets_materialized` | `cli` | `task`, `actor`, `names` |
| `issue.promoted`, `issue.linked` | `cli` | the request plus `task` |
| `merge.local` | `cli` | `repo`, `sha`, `tasks` |
| `doc.owner_changed`, `doc.deleted`, `doc.undeleted` | `cli` | `doc`, `actor`, `request` or `justification` |
| `wl:DocumentSubmitted`, `wl:DocumentAccepted` | `cli` | document, status transition where there is one |
| `task.gap_found`, `fix.started`, `fix.finished` (`outcome`: `resolved`, `substantive`, `escalated`), `doc.patched`, `doc.stale` | `cli`, `watcher` | the escalation ladder (05-documents.md) |
| `crew.member_added`, `crew.member_removed` | `cli`, `web` | project, actor, roles |
| `gchat.crew_space.member_skipped` | `system` | project, actor, reason |

Emission is a thin typed helper per event type, checked against generated code at compile time.

### 9.6 The `doc-lifecycle` subscriber

Two rules, hardcoded in Go behind `Evaluate(event) → []Action`:

| Event | Action | Guard |
|---|---|---|
| `wl:DocumentSubmitted` | mint `kind = 'review'`, state `ready`, `about_doc` set | no open review task about the document |
| `wl:DocumentAccepted` where the document is a spec | mint `kind = 'design'`, state `ready`: decide how to decompose this spec into plans, and write them | no open design task about the document |

Minted tasks take the document's project and carry `prov:wasInformedBy` to the causing event. Idempotency has two layers: redelivery is refused by the action's own deterministic `external_id`; a legitimate second acceptance while the planning task is open is absorbed and noted on it (`task.updated`), and once closed a further acceptance mints a fresh task. Submission is an event and changes no document column: the open review task is what "under review" means. A subscriber action must not emit an event its own subscriber consumes. The subscriber loop starts only when the server is given a background context.

Planning cost lands on the planning task: the planning skill claims the minted `design` task into a worktree before writing, so `lode task cost` answers for planning as for a feature. Tokens spent before the claim stay unattributed.

### 9.7 Metrics

| Metric | Type | Labels |
|---|---|---|
| `worklode_event_subscriber_lag` | gauge | `subscriber` (horizon offset minus `last_acked_offset`) |
| `worklode_events_processed_total` | counter | `subscriber`, `type`, `outcome` in `applied|suppressed|error` |
| `worklode_event_batch_duration_seconds` | histogram | `subscriber` |
| `worklode_event_streams_active` | gauge | none |
| `worklode_event_stream_events_sent_total` | counter | none |
| `worklode_watcher_actions_total` | counter | `rule`, `outcome` |

`type` is bounded by the generated set; unknown types count as `other`. Conventions are in 01-system-and-deployment.md.

## 10. Webhook-driven delivery

Handlers record facts. All lifecycle rules live in one resolver. Arrival order of GitHub and Flux events never matters.

### 10.1 Fact tables

Written inside the same `RecordEvent` transaction as the webhook that produced them, as natural-key upserts (`ON CONFLICT DO NOTHING`).

| Table | Columns | Meaning |
|---|---|---|
| `task_commits` | `task_id, repo, sha, source, seen_at` | attributes commits to tasks. Sources: pushes to task branches (pattern from `LODE_BRANCH_TEMPLATE`), PR correlation (head ref or `Worklode-Task:` body), the `Worklode-Task:` commit trailer, task-key references in default-branch commit messages, and `local_merge` from the git-side hooks |
| `main_commits` | `repo, sha, seq, pushed_at` | every default-branch push appends its commits in order. Main is linear in push order, so "X is included at Y" is `seq(X) <= seq(Y)`. A task's landed seq is the seq of the main commit that matched it |
| `env_deploys` | `repo, environment, main_seq, gh_status, flux_status, flux_seen, updated_at` | the per-environment deployed frontier, environment normalized to `dev`/`prod` |
| `release_frontiers` | `repo, tag, main_id, published_at` | the seq a published release covers; forward-only per tag |

A frontier is confirmed at seq N once both the GitHub `deployment_status: success` and the Flux `ReconciliationSucceeded` signals are present; every task with landed seq at most N is covered. Where no Flux revision has ever correlated for a repo/env, the GitHub signal alone confirms; the first matching Flux revision latches `flux_seen` and the pair requires both signals permanently. The latch never releases on its own; the handler logs `flux delivery gating latched`, and the repair is a `flux_seen` reset in the database. A deployment SHA on main resolves to its seq directly; a `last-deploy/*` SHA resolves through the `main-sha:` trailers of its cherry-picked commits.

A release frontier is the release's `target_commitish` when that resolves (through the GitHub App, for branch names) to a known main commit, so a backport tag covers only what it contains. Otherwise it falls back to main's head at webhook arrival. The release artifact records the resolved commit directly.

Environment normalization applies to GitHub environment names only: `dev`, `test`, `development`, `staging` are dev; `prod`, `production` are prod; everything else is ignored. `LODE_CLUSTER_ENV_MAP` (cluster to stage, for Flux) is operator config validated at startup to contain only `dev` and `prod`.

### 10.2 Handlers

All webhook handlers use HMAC signature checks and the idempotency of `RecordEvent`. Ingest is described from the identity side in 02-identity-actors-and-secrets.md.

| Handler | Behaviour |
|---|---|
| `push` | routed by ref: a task branch inserts `task_commits`; the default branch appends `main_commits` and sets landed seqs; `last-deploy/<env>` records deploy-branch SHA to main seq |
| `deployment_status` | normalizes the environment, resolves the SHA to a main seq, upserts `gh_status`. A SHA unknown on main is dropped and self-heals on the next deploy |
| Flux | resolves the revision SHA to `(repo, main seq)`, updates `flux_status`; failures mark the attempt failed. A revision matching no repo records nothing and does not latch |
| `pull_request` | merged-PR handling records facts only; the transition is the resolver's |
| `release` | creates the artifact and records the release frontier |
| `registry_package` | a published container version mints a `docker_image` artifact keyed by image name and tag with its OCI digest. Other package types and untagged versions are events without artifacts. Resolves no frontier |
| `POST /api/v1/merges` | the local reporter: `{repo, sha, tasks[]}` from `lode-hook post-merge`/`post-commit`. Appends the main commit, attributes it with source `local_merge`, resolves. Carries an actor, requires the task-mutation permission. `worklode_local_merge_reports_total{result}` counts `advanced|duplicate|unknown_task`; a steady stream of duplicates is what a healthy webhook plus clone pair looks like |

**Resolver.** `ResolveDelivery(tx, taskID)` reads the task's landed seq, env frontiers and release frontier, works out the furthest milestone the facts support along the repo's `done_state` branch (§3.3), and issues forward-only transitions, several in one resolve when signals arrived out of order. Every handler calls it for affected tasks at the end of its apply. It returns early for a task with children and for a deleted task.

Delivery advances a task through the repo its own commits landed in, so shared repos need no special handling and project-to-repo links play no part. A task spanning several repos tracks delivery through its primary repo only.

GitHub App permissions: Actions: read, Deployments: read, Contents: read, Packages: read; webhook subscriptions `push`, `deployment_status`, `registry_package` beside the existing ones. Flux notification-controller in every cluster gets a Provider/Alert pointing at `/hooks/flux`.

### 10.3 Error handling and limits

A failed correlation never fails a delivery. An unresolvable SHA is dropped; there is no pending-facts store. Facts are idempotent under redelivery and transitions are forward-only. Known limits:

- Push payloads carry at most 2048 commits. A landing commit outside the window is never attributed and the task strands at `in_review`. An `after` value missing from the `commits` array proves truncation; that increments `worklode_webhook_push_truncated_total` and logs repo, ref and range. The carried commits still apply.
- Pushes over 25 MB are not delivered by GitHub at all.
- The local merge reporter asserts delivery before a push, so a later `git reset --hard` leaves a fact for work that exists nowhere. Nothing retracts a fact once recorded. It also needs the task branch to still exist and does not see a rebase.
- Per-artifact delivery (a repo shipping an image and a library) is not modelled; one `done_state` per repo.

Surface: the states flow through task JSON, `lode task list` filters and cockpit badges. The task timeline shows landed on main at `<sha>`, dev deploy confirmed, prod deploy confirmed, released in `<tag>`. `lode project repo add` prints the discovered delivery profile.

## 11. Decisions

A `decision`-kind task is an undertaking with one accountable assignee: a scope approval, a go/no-go, a choice between framings. Its questions are rows in `decisions`, one or more per task. It closes when its last row is answered, in the same transaction as that answer. It has no code, branch or PR, so it is never leased: `lode task assign` names the decider, who starts and closes it (§13.5). `lode task claim` on one is an error. Where an answer needs a durable rationale, the decision `blocks` the `design` task that records it in a document.

```sql
CREATE TABLE decisions (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id          text NOT NULL REFERENCES tasks(id),
    key              text NOT NULL,              -- stable within the task
    position         int  NOT NULL,              -- authored order
    "group"          text NOT NULL DEFAULT '',
    question         text NOT NULL,
    context          text NOT NULL DEFAULT '',   -- markdown
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select','multi_select','single_select_notes',
                         'pick_or_freetext','yes_no','freetext')),
    options          jsonb,                      -- [{label, description}]; null for yes_no/freetext
    min_picks        int, max_picks int,         -- multi_select only
    answer           jsonb,                      -- {picked: [...], notes, freetext}; null until recorded
    decided_by       text REFERENCES actors(id),
    decided_at       timestamptz,
    UNIQUE (task_id, key)
);
```

A row is addressed as `<task>/<key>` (`WL-643/x-distribution`). Any task may carry rows; on a non-decision kind they are attached context and do not gate closing. `response_type` is fixed when the question is posed. A `yes_no` answer is `{"value": "yes" | "no" | "unsure"}`, so a decider with no view records that rather than leaving the row open. Writing `answer`, `decided_by` and `decided_at` is one transaction. An unanswered row may be reworded, re-ordered or moved to another task; an answered row is immutable, and deciding again is a new row or a new task. A rally carries no decision rows (`AddDecision` rejects it). A decision task needs no approval by default; a flow may still require one (§13.7).

## 12. Deleting tasks and documents

`abandon` is the preferred close and records that work was considered and dropped. `delete` is a narrower close for a row that should never have existed: a mistyped `lode task add`, a duplicate import, dev re-seeding. Delete is a tombstone, orthogonal to state. It is not a state value, and the transition set is untouched.

| | `abandon` | `delete` |
|---|---|---|
| Says | considered and dropped | should not have existed |
| Is | a task state | a tombstone |
| Applies to | tasks | tasks and design documents |
| Leaves | a visible `abandoned` row | a hidden row and its events |
| Reversible by | reopen | undelete |

Tasks and documents each carry `deleted_at timestamptz`, `deleted_by text`, `delete_justification text`, all null on a live row and set together. `deleted_at IS NULL` is the whole predicate. Undelete clears all three. Both emit events (`task.deleted`/`task.undeleted`, `doc.deleted`/`doc.undeleted`) with the justification in the payload. Nothing is ever removed: events, `state_log`, edges and artifacts stay valid.

Three things move with the tombstone, in the deleting transaction: the lease is released and an `in_progress` task goes back to `ready`; the parent's roll-up is recomputed, on delete and on undelete; a document's slug and corpus number stop being reserved (uniqueness is over live rows, and a live row always wins over a tombstone that shares its slug). Delete does not cascade to children or covering plans.

**Justification by instance environment.** `LODE_INSTANCE_ENV` (01-system-and-deployment.md) decides: on `prod` a non-blank justification is required and its absence is a 422 naming the environment; on `dev` it is optional. The rule is enforced server-side. Undelete needs no justification anywhere.

**What a tombstone hides.** `deleted_at IS NULL` joins the `WHERE` of every list, board, ranking, claim, roll-up, delivery resolution, cockpit page, document list, search and projection. Fetching by id succeeds and renders the tombstone (actor, time, justification). Events stay in `lode event tail`. Edges stay, and a deleted blocker stops blocking because the blocking check reads live tasks. The knowledge graph shows live rows only and drops a deleted row on the next projection pass; the tombstone is not projected, because the event log owns provenance. Deleted is not closed: `taskClosed` stays "delivered or abandoned", so a deleted `draft` reports `closed: false`. A mutation addressed to a deleted row by id still applies.

Surface: `DELETE /api/v1/tasks/{id}`, `DELETE /api/v1/docs/{id}` with optional `{"justification": "..."}`; `POST …/{id}/undelete`. All four are in `routeGuards` and need the permission the entity's other mutations need; there is no delete-specific role. `lode task delete <id> [--justification …]`, `lode doc delete <ref> [--justification …]`, `lode task undelete`, `lode doc undelete`. No `-j` shorthand. `lode task list --deleted` and `lode doc list --deleted` show tombstoned rows instead of live ones. Metric: `worklode_deletes_total{entity, op, outcome}` in `internal/api`, `outcome` in `ok | justification_required | not_found | error`.

## 13. Research work

Journalists and data scientists working the Sunstone Way (Discovery through Report) use the same backbone: the same event log, project scoping, and claim machinery where agents are involved, plus milestones, deliverables, participants and approvals. Story and Distribution add no new entities.

### 13.1 The investigation is a project

Each investigation is one project. Its key prefixes every identifier and its lifecycle is the investigation's. Research projects own no git repo. The research monorepo maps once to a standing umbrella project, which opens the webhook gate; inside it each investigation directory carries `.worklode/config.toml` with `current_project`, nearest config wins. Correlation matches on task id alone, so a PR on `COW-7-slug` attaches to `COW-7` whatever project the repo maps to.

The Sunstone stage is a query derived from governed decisions (`decision` tasks), milestones, deliverables and work; it is never an editable column. Entering Research is an explicit lead decision. After Research, Worklode may recommend a transition and the lead confirms it. Advancing with unfinished work needs a stated reason; that work stays on its milestone as carryover. Returning to an earlier stage appends another reasoned transition event. Once every required deliverable is terminal Worklode may recommend closure, but the lead closes explicitly after reviewing unfinished items. Closure ends the bounded project and its active Crew.

### 13.2 Milestones and deliverables

```
project ── milestone ──┬── task ── subtask
                       └── deliverable
```

Milestone and deliverable are entities with their own tables. A milestone stores identity, title and ordering; its progress is a query over its tasks and deliverables. A deliverable cannot be claimed, worked or closed. A task references its milestone through nullable `milestone_id`; a task with no milestone is ongoing maintenance, legal everywhere and the norm for engineering projects. A decision attaches to a milestone like any task.

The default shape for `kind=sunstone-story` projects, minted at promotion: milestones **internal review** and **publication**, with deliverables dataset/data product, reproducible analysis, methodology, scientific report and story. Named, versioned instance configuration supplies the template; a project receives a snapshot, so a later edit cannot change its definition of done.

### 13.3 Deliverables

A deliverable is a declared, checkable output: a datapackage, a report PDF, a CMS post, a docker image, an Iceberg table, a named graph. It declares how it is verified: **by address** (a GCS URL, CMS slug, table name) or **by label** when the address is minted at build time (`worklode.deliverable=COW/datasets`), with skills turning the label into deterministic lint checks. Humans never hand-link artifacts. A custom deliverable has exactly three descriptive fields: name, description, optional URL; its evidence and approvals are separate governed rows.

State is **reported**: closing a task asserts nothing about it. **Push**: CI, the CMS and the data pipeline report over the API, each an ingest source (signed or bearer-authenticated, one `RecordEvent` per fact, idempotent by `(source, external_id)`). **Poll**: a separate prober process (the `lode-watch` pattern, own deployment and token) checks declared addresses. "Is the project published" is the query "every deliverable published". Every fact records how it became known: **observed** (emitter or prober) or **user-reported** (an authenticated actor where no integration exists). The UI says "User-reported". A user-reported fact is auditable and does not stand in for verification. `wl:Deliverable` is the graph's definition of done made concrete; a deliverable without an artifact is `wl:Effect` (07-knowledge-graph-and-search.md). Done-verification of deliverables is in 04-done-verification-workflows.md.

### 13.4 Identifiers and cross-project references

Every entity kind draws from its own per-project sequence, from `(project, kind)` counter rows:

| Entity | Form |
|---|---|
| Task, subtask | `COW-7` |
| Milestone | `COW-MILE-2` |
| Deliverable | `COW-DEL-3` |
| Spec, plan | `COW-SPEC-4`, `COW-PLAN-7` |

Numbers are unique per kind. Only tasks are claimable, so only bare `<KEY>-<n>` ids appear in branches, trailers and merge correlation. `lode show <id>` dispatches on the type segment, with an equivalent `--kind` spelling.

Containment never crosses a project. Two references do: `blocks` between tasks, and a milestone depending on a deliverable produced under another project. Plus `seeded_by`. References are rows in one typed edge table `(from_kind, from_id, to_kind, to_id, rel)`. There is no unified entities table.

### 13.5 People

**Assignee** (one, nullable) is ownership of a task or decision, separate from leases, with a lease-free start/stop/submit lifecycle and auto-assign on start: `lode task assign`, `start`, `stop`, `submit` emit `task.assigned`, `task.started`, `task.stopped`. A joint task splits rather than sharing an assignee.

**Participants** are stored per project with role labels; the UI calls the set the **Crew**. Roles are a fixed CHECK-enforced vocabulary: `member` (default), `editor`, `science-lead`, `reporter`, `domain-expert`, `data-scientist`, `engineer`. Exactly one participant is the project lead. Agents, advisory-only approvers and notification recipients are never silently added. Any Crew member may add or remove an ordinary member; every change is an event (`crew.member_added`, `crew.member_removed`, emitted from every mutating call site). Before removal, every open task, decision and review the member owns is reassigned or explicitly left unassigned. Changing the lead requires acceptance by outgoing and incoming leads; if the outgoing lead is unavailable, the Editor and Science Lead jointly authorize. A project may designate one **deputy**, set on add, revoked by remove and re-add, mutually exclusive with lead, with full lead authority whenever the lead does not act; the roster shows it as the virtual read-only role `acting-lead`, which cannot be set as a role. An external expert may be an invited participant without a Keycloak actor: shown in the Crew, unable to own work, resolve an approval or lead until linked, and linking preserves the history. Closing the project closes the active Crew but keeps roster, role history and contributions.

**Contributors** are derived: everyone ever assigned a task in the project.

Identity: Keycloak is the human identity (02-identity-actors-and-secrets.md). Stored actor fields (`groups`, `email`, `expected_github_login`) and the login upsert are defined in 02-identity-actors-and-secrets.md §1 and §4.3. Gates check group membership by name. Stored groups go stale between logins, which is why approving is a web-session act.

### 13.6 Approvals

One `approvals` table serves every approval (deliverables, documents, dossiers, PRs, tasks), keyed to `(entity_kind, entity_id, subject_revision)` and carrying the required role (a Keycloak group name) or named actor, the resolving actor, timestamps and state:

```
awaiting → changes_requested → awaiting (on re-request)
awaiting → approved | rejected      -- final by convention
```

A requirement is materialized as an `awaiting` row when the entity is created, so a missing approval is a visible row and "what is waiting on whom" is a query. `subject_revision` is the immutable revision the approver saw: a document revision, an analysis commit, a PR head, a deliverable evidence revision. A material change reopens the changed target only; dependent objects get an explicit impact review (downstream owner supplies an impact note, a qualified prior approver confirms or reopens). Approval by the author is disallowed by default; a self-review exception needs the effective policy to allow it and a different authorized actor to approve the exception first, both event-logged. Rule-created rows are owned by the system `worklode` actor.

**Where requirements come from.** Promotion stamps labels; a rule matching `kind=sunstone-story` sets the project's `approval_flow`; the named, versioned flow declares which entity kinds need which role's sign-off. Flows live in instance configuration with pre-baked defaults, and the project stores a snapshot. The default story flow keeps three lanes independent: reproducible analysis (PR review per task policy plus one analysis-level peer decision on an exact commit, peer chosen by the reviewer template and distinct from the author), methodology (Science Lead and domain expert on an exact revision), scientific report (buddy, expert and journalist on an exact revision). One session may present several targets but records separate decisions. Tasks have no review requirement by default. Ad-hoc requirements can be added to any governed target. An analysis submission is a revision-bound evidence bundle (repo and commit, environment lock, entry point, tests, dataset snapshots and lineage, outputs, diff from the last reviewed revision); later commits do nothing until the deliverable designates a newer one. Specs always pass explicit review before acceptance; plans and tasks are optional.

**Gates.** Approving is a web UI act (fresh OIDC group claims). CI's manually triggered prod publish workflow queries Worklode and fails without the approval. The CMS publish button fails without the story's approval, opt-in per post, degrading to advisory when Worklode is unreachable. GitHub `pull_request_review` ingest writes into the same table: an `awaiting` row when a task-correlated PR opens, resolved when the review lands. GitHub stays the review surface.

### 13.7 Intake

Ideas enter a standing **intake project** at Discovery. Capture needs a title and description; everything else is prose in the description. The pitcher is responsible for fact-checking an LLM-drafted pitch. An AI analysis may file an unowned pitch; a named human must adopt it before Selection.

An idea remains a task. Selection builds a versioned **dossier** around it: litmus-test results, claims, sources, unknowns, hypothesis changes, recommendation, the exact AI run and its audit path. The dossier is a backbone-native revisioned document without section anchors, crit review or acceptance lifecycle; every edit lands a new immutable revision. Two explicit human decisions: accept Gate 1 and authorize bounded pre-research, then Gate 2 after the AI-assisted work. Editorial Evaluation records separate Editor and Science Lead decisions on the exact dossier revision (`entity_kind='dossier'` in `approvals`); both must approve. A rejection blocks promotion without closing the dossier, and the rejecting role owns the next move. Overriding the AI recommendation needs a rationale and both approvals. Editing after a decision reopens on the new revision.

Passing Editorial Evaluation **promotes** in one transaction: create the project, stamp labels and `seeded_by`, mint the configured milestones and deliverables, snapshot the approval flow, record the initial Crew and lead, close the intake task. Killed ideas cost one closed task.

### 13.8 Events out, events in, crew spaces

No producing handler gains a hardcoded notifier; every outbound consequence is an offset-tracked subscriber (§9.4). Orchestrated work sends no email or chat message; the first human-facing consequence is the per-user Morning Brief in the cockpit, derived from lifecycle events, with an explicit **Reviewed through now** cursor (10-cockpit.md). New ingest sources (CMS publish transitions, CI publish reports, pipeline registrations) each bring their own `events.source` value; the CMS also records who approved and published.

The `gchat-crew-space` subscriber reacts to `crew.member_added` (create the space if `projects.chat_space_name` is null, then add the member) and `crew.member_removed`, acking only after its Chat API calls succeed. Members are invited by `actors.email`; a missing email, an address outside the configured Workspace domain, or an unlinked invitee is skipped and logged as `gchat.crew_space.member_skipped`. Space name is `"{Key} {Name} Crew"`. `chat_space_name` is the idempotency guard. Auth is a dedicated Chat App identity on Workload Identity with space-create and membership scopes. Archiving on close, lead manager status and role reflection are out of scope.

## 14. Postgres and data layer

Driver `github.com/jackc/pgx/v5` through `database/sql` (`stdlib`, name `pgx`), so store functions are `*sql.Tx`-typed. `Open` returns a real pool (`SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`). The clock is injectable (`SetNowFunc`). Migrations are golang-migrate with the `database/postgres` driver, embedded via `iofs`. Foreign keys are enforced.

Isolation is **READ COMMITTED**. Claim correctness rests on the `FOR UPDATE` row lock plus the `leases_active` unique backstop, so SERIALIZABLE is not needed. Postgres runs as a CNPG cluster; migrations run as an init container or Job before the server starts (01-system-and-deployment.md). The sweep loop and migrations are safe under multiple replicas because every effect is idempotent through the event log; the subscriber loops elect one consumer with an advisory lock. Specs 006 and 007 own the meaning of the observed tables (`issues`, `pull_requests`, `ci_runs`, `reviews`, `artifacts`, `deployments`, `runtime_events`) that share the schema.

Typed errors: `ErrNotFound`, `ErrInvalidInput`, `ErrLeased`, `ErrBlocked`, `ErrBadTransition`, `ErrCycle`, `ErrEdgeExists`. Unique violations are detected through `pgconn.PgError.Code == "23505"`.

## 15. Permissions

Every route here is named in `routeGuards`. Claim, renew, release, reopen, `set state`, abandon, edge, decompose, delete, undelete and `POST /api/v1/merges` need the task-mutation permission; project creation and repo mapping need the admin-side permission; webhook routes are open and HMAC-signed. Roles and the grants table are defined in 02-identity-actors-and-secrets.md. There is no MCP surface: agents drive `lode --json` directly.

## Sources

WL-SPEC-4 (execution backbone, with the folded 025 §10 and §15 amendments and the 045 restatements), WL-SPEC-5 (prioritization and pickup), WL-SPEC-44 (deleting tasks and documents), WL-SPEC-29 (research work: projects, milestones, deliverables, approvals).

## Open questions

- Whether a manual `lode task set state merged` should stop closing the lease, as delivery transitions already do (WL-SPEC-4 §5.1).
- Whether `concern` becomes required at task creation once the enum stabilises (005 §8.3).
- Whether cross-project `blocking_fan_out` should be weighted when `--project` is omitted (005 §8.4).
- Whether a bidirectional Task to GitHub Issue mirror is created eagerly or lazily, and which side wins on divergent edits (WL-SPEC-4 §12.5).
- Whether GitHub is ever replaced as the PR review surface (029 §7.3).
- Per-project access control: every logged-in user sees every project today (029 §9).
