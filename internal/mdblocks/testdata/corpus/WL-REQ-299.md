
The inbox is otherwise fed only by live webhooks, so everything before a repo was mapped would stay invisible. `lode inbox import` fetches a repo's issues and PRs server-side and upserts them as inventory.

| Decision | Choice |
|---|---|
| Where it runs | Server-side, which holds the App installation token |
| Request shape | Synchronous, page-capped, no job table |
| Write path | `store.UpsertIssue` / `store.UpsertPR`, unchanged |
| Lifecycle replay | Never. Import writes rows and drives no `Transition`, `CloseActiveLease`, `InsertTaskCommit`, or `ResolveDelivery`; replaying history would resolve delivery for tasks never in flight and roll it into parents. |
| Default selection | `--state open`, issues only |
| Authorization | admin, like `lode project repo add` |
| Idempotency | `(repo, number)` upsert; promoted rows untouched |
| Linking an issue to a task | `lode inbox link`, the third triage verb |
| `triage_state` for a link | `promoted` |
| Event-subscription mismatch | warn on `project repo add`, never gate |

Correlation still happens inside `UpsertPR` (head ref, then body) and no-ops when the task does not exist.

