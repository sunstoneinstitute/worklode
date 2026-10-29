
| Actor | Kind | Use |
|---|---|---|
| `sandbox` | `agent`, never admin | Every piece of work a human launched that runs under a task-scoped token: interactive sessions, sandboxes dispatched from a laptop or from CI on someone's behalf. Auto-provisioned on first task-token mint. The launching human is named in the token's description (WL-REQ-48). |
| `watcher` | `agent` | `lode-watch`'s pod informer, which reports runtime events, and the server's document-lifecycle subscriber, which mints review and planning tasks. |

Ids name the dispatch mechanism and its policy: stable, lowercase, no vendor name, so replacing the harness behind a mechanism is a configuration change. A new actor joins the roster only by the test of WL-REQ-47.

