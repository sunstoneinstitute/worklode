
The table below is the built-in `default` workflow. The authority on any task's legal transitions is the per-project workflow that governs it: the mandatory core edges hold everywhere, and entries into `in_review` and the delivery states exist only where the workflow declares them. Workflows are defined in WL-RULE-141.

| From | To | Trigger |
|---|---|---|
| `draft` | `ready` | publish (`lode task publish`) |
| `ready` | `in_progress` | claim (lease acquired), or start on the lease-free path |
| `in_progress` | `in_review` | submit for review |
| `in_progress` | `ready` | release, or lease expiry |
| `in_review` | `merged` | accept |
| `in_review` | `in_progress` | rework |
| `ready`, `in_progress`, `in_review` | `merged` | landing on main (resolver), or `lode task set state merged` |
| `merged` | `deployed_dev`, `deployed_prod`, `released` | delivery facts (WL-RULE-112) |
| `deployed_dev` | `deployed_prod`, `released` | delivery facts |
| `ready`, `in_progress`, `in_review` | `completed` | `lode task complete` |
| `draft`, `ready`, `in_progress`, `in_review` | `abandoned` | `lode task abandon` |
| `merged`, `completed`, `deployed_dev`, `deployed_prod`, `released`, `abandoned` | `ready` | `lode task reopen` |

`Transition(tx, now, taskID, from, to, eventID)` is the one guard. Inside the caller's transaction it verifies both that the move is in the transition set and that the task's current state equals `from`, bumps `updated_at`, appends a `state_log` row attributed to `eventID`, and finally resolves the parent's roll-up if the task has one (WL-REQ-92). Unknown task gives `ErrNotFound`, wrong from-state gives `ErrBadTransition`.

Delivery transitions are forward-only. The resolver never walks a task backward and never advances a `draft` or a `completed` task: a commit that later lands for a `completed` task leaves its state alone. `deployed_prod` and `released` are peers: neither is ahead of the other, and there is no transition between them.
