
Six states are mandatory in every workflow: `draft`, `ready`, `in_progress`, `merged`, `completed`, `abandoned`. A workflow that omits any is invalid. The **core edges** are defined over the whole vocabulary and legal in every workflow:

| Core edge | Meaning |
|---|---|
| `draft -> ready` | publish |
| `ready -> in_progress` | claim / start |
| `in_progress -> ready` | release, lease expiry, stop |
| `in_review -> in_progress` | rework |
| `ready`, `in_progress`, `in_review` `-> merged` | work landed, or the manual close |
| `ready`, `in_progress`, `in_review` `-> completed` | finished without landing code; refused for a task with any pull request (WL-REQ-1884) |
| `draft`, `ready`, `in_progress`, `in_review` `-> abandoned` | abandon |
| `merged`, `completed`, `deployed_dev`, `deployed_prod`, `released`, `abandoned` `-> ready` | reopen |

Every core edge targets a mandatory state. Invariant: **a task can always leave its state along a core edge. A task can only enter an optional state its workflow declares.** This is also the stranding rule: a task left in a state its workflow no longer declares still has its core exits.
