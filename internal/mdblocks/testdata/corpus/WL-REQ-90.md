
| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | manual publish |
| `ready` | `in_progress` | any child started or closed, including abandoned |
| `in_progress` | `merged` | every child closed, at least one `merged` or later |
| `in_progress` | `completed` | every child closed, none `merged` or later, at least one `completed` |
| `in_progress` | `abandoned` | every child abandoned, or manual abandon |
| `merged`, `completed` | `ready` | a child reopens |

`ResolveHierarchy(tx, now, parentID, eventID)` reads the children and applies the table. Zero children: nothing fires. All abandoned: `abandoned`. Abandoned and `completed` children only: `completed`. Any `merged` or later child among closed children: `merged`. A child back to `ready` puts the parent back to `ready`.

`ResolveDelivery` returns early for a task with children. A manual `set state merged` or `lode task complete` is refused.
