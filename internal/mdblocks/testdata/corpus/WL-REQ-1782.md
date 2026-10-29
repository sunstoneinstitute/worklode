
Hover text appears on every cell and reference after a short delay and
never captures the pointer. A section cell: `§3.1 Renewal · In progress ·
WL-PLAN-99`. A task cell: id, title and its **position**, the first line that
applies:

| Position | Fact |
|---|---|
| `merged`, `deployed to dev`, `deployed to prod`, `released` | the task is landed; this outranks any PR or CI fact |
| `queued for merge` | the PR has an open merge-queue entry |
| `checks failed on PR #N` | latest CI run for the PR's head SHA concluded `failure` |
| `checks running on PR #N` | latest CI run is `in_progress` or `queued` |
| `PR #N open` | an open PR carries the task id and neither line above applies |
| `in review` | task `in_review`, no PR fact |
| `claimed by <actor>, <age>` | task `in_progress` with a live lease; age in minutes, hours or days |
| `assigned to <actor>` | task `ready` with an assignee |
| `ready` | task `ready` |
| the task state | anything else |

The PR number is a link only when the tooltip is pinned by a click.

