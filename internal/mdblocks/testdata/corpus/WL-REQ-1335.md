
A plan **covers** a section when one of its covers edges reaches a rule the
section arranges, directly or through that rule's supersession chain
(WL-SPEC-77 §4).

**Plan state** comes from document status and minted tasks (`tasks.plan_doc`).
A `withdrawn` plan is left out of the derivation: it covers nothing. A `stale`
plan counts as `accepted` (plan lifecycle, WL-SPEC-77 §9). Abandoned tasks are
ignored. Task states fall into three classes by rule over
`model.TaskStates`, and a test pins that the classes plus `abandoned` partition
the set: **landed** (states with a delivery rank: `merged`, `deployed_dev`,
`deployed_prod`, `released`), **active** (past `ready`, short of landed:
`in_progress`, `in_review`), **unstarted** (`draft`, `ready`). Open means
active or unstarted.

| Plan state | Condition |
|---|---|
| `draft` | document status `draft` |
| `built` | status `superseded` or `executed`, or `accepted` with at least one task and every task landed |
| `in_progress` | `accepted`, at least one task landed or active, at least one not landed |
| `not_started` | `accepted`, tasks minted, none landed and none active |
| `no_record` | `accepted` and no minted task |

**Section state** takes the state of the furthest-along covering plan, in the
order `built`, `in_progress`, `not_started`, `no_record`, `draft`.

| Section state | Label | Condition |
|---|---|---|
| `built` | Built | best cover is `built` |
| `in_progress` | In progress | best cover is `in_progress` |
| `not_started` | Accepted, not started | best cover is `not_started` |
| `no_record` | Accepted, no execution record | best cover is `no_record` |
| `draft` | Plan awaiting acceptance | every cover is `draft` |
| `unplanned` | Unplanned | no plan covers it |

A section whose rules are all informative is not owed: it counts in neither
the section bar nor the group counts and does not place its spec in a group.
Every other section counts.

**Grouping.** A spec lands in the first group whose condition holds: Active
(any section `in_progress` or `not_started`), Needs planning (any `unplanned`
or `draft`), No execution record (any `no_record`), Built (every section
`built`). Within a group specs sort by most recent document update. Each row
ends with the first applicable **next act**: `N open tasks in <plans>` (every
such plan named in full); `accept <plans>` (every draft plan named);
`N sections unplanned`; `no execution record for <plans>`; `nothing
outstanding`.
