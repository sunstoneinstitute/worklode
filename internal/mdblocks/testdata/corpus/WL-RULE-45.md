
An actor is an accountable principal with credentials. An actor row does four things: it holds tokens, it can be granted or denied a permission by the `grants` table, it holds leases and assignments, and costs roll up to it. Nothing about an actor describes capability.

| Column | Meaning |
|---|---|
| `id` | Stable text id. Humans: Keycloak `preferred_username`. Agents: the dispatch mechanism (`sandbox`, `watcher`). |
| `kind` | `human`, `agent` or `service`. No other kind exists. |
| `display_name` | Humans: the Keycloak `name` claim. |
| `admin` | Boolean. Humans: re-synced from the Keycloak `admin` role on every login. Agents: never admin. |
| `groups` | Humans: the full `groups` claim from the last login, stored as JSON. Gates check group membership by name at request time. Stored groups go stale between logins. |
| `email` | Humans: the `email` claim from the last login. Kept for inviting crew members to a project's Google Chat space, which is designed, not built (WL-SPEC-75, Designed, not built). |
| `github_username` | Humans: the Keycloak `githubUsername` attribute, re-synced on every login. Unique case-insensitively across actors. Used to link a GitHub account at login (section 9) and to match pull request authors to actors. |

Actor ids are foreign keys from `tasks.created_by`, `tasks.assignee`, `tokens.actor_id`, `leases.actor_id` and `project_participants.actor_id`. An id is therefore permanent.

