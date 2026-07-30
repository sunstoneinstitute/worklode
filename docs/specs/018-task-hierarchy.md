# Spec 018 — Task hierarchy (epics and tracking tasks)

**Date:** 2026-07-29 · **Status:** design ·
**Umbrella:** `000-umbrella-architecture.md`
**Area:** spec 004 (execution backbone — task edges), spec 005 (pickup — the
decomposition gate), spec 011 (delivery lifecycle — the state machine)

## Why

`child_of` is half-built. The edge type exists and is cycle-checked, the HTTP
API accepts it, and the web task page renders Parent/Children — but nothing
writes it and no decision the system makes consults it.

| Layer | `child_of` today |
|---|---|
| Schema | `task_edges.type IN ('child_of','blocks')`, `UNIQUE (from_task,to_task,type)` (`0001_baseline.up.sql:65`) |
| Store | `AddEdge` (`tasks.go:401`) cycle-checks child→parent via `reachesViaChildOf` (`tasks.go:443`) |
| API | `POST`/`DELETE /api/v1/tasks/{id}/edges` (`server.go:267`) |
| Web | Parent and Children on the task page (`web.go:167`, `:175`) |
| CLI | reads it (`lode task show` prints edges) — **nothing writes it** |
| Ranking | ignored: `readyCandidates` (`ranking.go:61`) filters on `blocks` only |
| Roll-up | none |

Two consequences. A long plan cannot be represented as a tracking task plus its
phases, so decomposition output arrives as a flat list of unrelated tasks. And
`needs_decomposition` (spec 005) is a dead-end flag: it removes an oversized
task from the pickup loop with no supported way to split it.

## Decisions

Taken here with rationale, pending sign-off.

| Decision | Choice |
|---|---|
| Container identity | Declared: `kind = 'epic'` |
| Parents per task | Exactly one (partial unique index) |
| Hierarchy depth | Max 2 edges (epic → task → subtask) |
| Epic claimable | Never — excluded from the ready set |
| Epic delivery states | Forbidden: `in_review`, `deployed_dev`, `deployed_prod`, `released` |
| Epic closure | Automatic roll-up, forward and backward |
| Progress | Derived on read, never stored |
| Cross-project children | Rejected in v1 |
| Child ordering | Out of scope |
| Blocker inheritance | Out of scope — hierarchy and blocking stay orthogonal |
| Parent kind | Must already be `kind = 'epic'`; `AddEdge` rejects any other parent (422) |
| Direct claim of an epic | Rejected in `Claim` as well as excluded from the ready set |

**Why declared rather than inferred from "has children":** inference means one
`AddEdge` call silently changes whether a task can be claimed and what a live
lease on it means. Declaring makes conversion an explicit act that can validate
its preconditions, and the ready-set exclusion becomes a column predicate.

**Why the parent must already be an epic:** it is what makes "declared" real.
`ready -> in_progress` is a legal epic transition — it is the roll-up trigger —
so excluding epics from the ready set alone would still let `lode task claim
<epic-id>` through; `Claim` carries the same guard. Two supported ways to get
an epic: create one (`lode task add --kind epic`) or convert in place (`lode
task decompose`). There is no `lode task edit --kind`.

**Why a depth cap:** the brief is a bounded-payload contract (`brief.go:9-19`)
and the tree walks that feed roll-up and breadcrumbs are unbounded without one.
Cycle detection already walks the chain; the cap is the walk length.

## Data model — migration `0006_task_hierarchy`

```sql
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic'));

-- A task has at most one parent. Two child_of edges out of one task are legal
-- today, and web.go:167 silently keeps whichever was inserted last.
CREATE UNIQUE INDEX task_edges_single_parent
    ON task_edges (from_task) WHERE type = 'child_of';

-- Child lookups (WHERE to_task = $1 AND type = 'child_of') have no usable
-- index: the unique constraint leads with from_task.
CREATE INDEX task_edges_children
    ON task_edges (to_task) WHERE type = 'child_of';
```

The `.down.sql` drops both indexes and restores the four-kind CHECK, failing if
any `kind = 'epic'` row survives.

## Epic semantics

### Never claimable

One predicate in the `readyCandidates` query (`ranking.go:62-72`):

```sql
AND t.kind <> 'epic'
```

The worktree is the unit of Worklode work (spec 008) and an epic has nothing to
check out, so `lode next` must never hand an agent a container. Decomposition
work that genuinely needs a worktree becomes a child task.

### Restricted state machine

An epic's state is driven entirely by its children. `legalTransitions`
(`tasks.go:66`) is global, so the epic restriction is enforced in `Transition`
as a kind-aware guard rather than by a second table.

| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | manual (`lode task ready`) |
| `ready` | `in_progress` | any child started or closed, including abandoned |
| `in_progress` | `merged` | every child closed, at least one delivered |
| `in_progress` | `abandoned` | every child abandoned, or manual `lode task abandon` |
| `merged` | `ready` | existing reopen path, when a child reopens |

`in_review`, `deployed_dev`, `deployed_prod`, and `released` are rejected for
epics. Those states are earned by observed deploy facts about a specific commit
(spec 011) and an epic has no commit. `ResolveDelivery` (`delivery_resolve.go:78`)
returns early on `kind = 'epic'` rather than relying on the commit join never
matching.

Reusing `merged` as the epic terminal keeps `closedStates` (`tasks.go:537`)
correct with no change, so a completed epic stops blocking whatever points at
it. `0005_delivery` deliberately removed the old `done` state; this spec does
not revive it. For an epic, read `merged` as "all children delivered".

### Roll-up

Two distinct mechanisms. Conflating them is the usual failure mode.

**Progress — derived, never stored.** `closed_children / total_children` over
direct children, computed on read for `lode task show`, `lode board`, and the
web page. Two counts, no migration, no resolver, no event-log noise.

**Closure — stored, one transition per event.** `ResolveHierarchy(tx, now,
parentID, eventID)` reads the parent's children and applies the table above.

Edge cases the resolver must get right:

- **Zero children.** No roll-up fires. An epic with no children stays where it
  is; it is a modelling mistake, not a completed epic.
- **All children abandoned.** Rolls up to `abandoned`. Treating abandonment as
  delivery would report cancelled work as shipped.
- **Mixed abandoned and delivered.** Rolls up to `merged` — some of the epic
  landed.
- **Reopen.** A child returning to `ready` puts the epic back to `ready` via
  the existing reopen transition. Asymmetric roll-up produces boards that lie.

## Roll-up hooks into `Transition`, not its callers

There are eleven `Transition` call sites across `internal/api`,
`internal/hooks`, and `internal/store`. Hooking each one would leave the
invariant one forgotten call site away from breaking.

Instead, `Transition` (`tasks.go:154`) ends with: if the task has a parent, call
`ResolveHierarchy` on it with the same `tx`, `now`, and `eventID`. The child's
own event is the correct attribution for the parent's derived move, so the
timeline explains itself with no synthetic event.

Recursion terminates: a subtask resolves its epic, the epic resolves nothing
(depth cap 2), and cycles are impossible by `AddEdge`'s existing check.

## API

- `POST /api/v1/tasks/{id}/edges` gains validation for `child_of`: reject a
  second parent (409, `ErrEdgeExists` shape), a cross-project edge (422), and
  an edge exceeding the depth cap (422).
- `AddEdge` (`tasks.go:401`) returns the walk length from `reachesViaChildOf`
  instead of a bool, and enforces the cap.
- `POST /api/v1/tasks` accepts `parent`, creating the task and its `child_of`
  edge in one transaction — no window where the child exists unparented.
- `GET /api/v1/tasks/{id}` gains a `hierarchy` object: `parent` (id, title,
  state, or null) and `progress` (`{closed, total}`), both derived.
- `POST /api/v1/tasks/{id}/decompose` — see below.

## CLI

Symmetric with the existing `block`/`unblock` pair:

```
lode task add --parent <id> …            create a child in one round trip
lode task parent <id> --under <epic>     adopt an existing task
lode task unparent <id>
lode task tree [<id>]                    hierarchy with per-epic progress
lode task list --parent <id>
lode task decompose <id> --into "A" "B"  see below
```

`lode task show` gains a `Parent:` line and, for an epic, `Progress: 3/7`.
`lode board` groups an epic's children under it.

## Brief — exactly one hop up

`store.Brief` (`brief.go:18`) gains `Parent *Task`, populated with ID, title,
and state only. An agent should know its task belongs to "Delivery lifecycle"
without spelunking; the full ancestry and the sibling list are both unbounded
and stay out. The field follows the reserved-shape convention already used for
`GoverningDesign`/`AffectedComponents`/`DefinitionOfDone`.

## Decomposition — closing the loop from spec 005

```
lode task decompose <id> --into "Title A" "Title B" "Title C"
```

One transaction: set `kind = 'epic'`, clear `needs_decomposition`, create the N
children inheriting project, priority, and concern from the parent, wire the
`child_of` edges, and leave the children `draft`. Rejected when the parent holds
an active lease — decomposing work someone is holding is a coordination bug.

This is what makes the `needs_decomposition` gate actionable: an oversized task
becomes its own tracking task plus the pieces, in place, keeping its id and
every reference to it.

## Testing

- Single parent: a second `child_of` out of one task is rejected; the existing
  `UNIQUE (from_task,to_task,type)` still rejects an exact duplicate.
- Depth: a third level is rejected; the existing cycle test still passes.
- Cross-project: rejected.
- Ready set: an epic never appears in `claim --next`, including when it is
  `ready`, unblocked, and top-ranked by every other factor.
- Roll-up forward: first child to `in_progress` moves the epic off `ready`;
  last child closed moves it to `merged`.
- Roll-up edge cases: zero children (no move), all-abandoned (`abandoned`),
  mixed (`merged`), child reopen (epic back to `ready`).
- Roll-up attribution: the parent's `state_log` row carries the child's
  `event_id`.
- Epic delivery states: `ResolveDelivery` leaves an epic alone even with
  commit and deploy facts attributed to it.
- Depth-2 recursion: a subtask closing resolves its task, which resolves the
  epic, in one transaction.
- `decompose`: creates children, converts the kind, clears the flag, and is
  rejected under an active lease.
- Brief: `Parent` populated one hop, absent for a root task.

## Out of scope

- **Child ordering / rank.** Roll-up and progress do not need it.
- **Blocker inheritance.** `blocks` edges compose already; children do not
  inherit an epic's blockers.
- **Cross-project epics.** Requires a roll-up and board model that spans task-id
  namespaces; revisit if a real multi-repo initiative needs it.
- **Epic-level estimates or burndown.** Progress is a count of children.
- **Graph projection.** The `wl:` vocabulary for hierarchy belongs to spec 006.

## Resolved

- **Q018.1 — Does an epic need wrap-up work?** No. Closure is automatic, and a
  final integration or documentation step is a child task rather than a reason
  to make closure manual. Revisit if real usage contradicts it.
- **Q018.2 — Should `lode task done` on an epic be an error or a manual
  override?** An error. `done` is `in_review -> merged` and `in_review` is
  forbidden for epics, so the kind guard in `Transition` rejects it with a
  message naming the roll-up rule. There is no override.
