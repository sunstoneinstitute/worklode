
The task edges of WL-REQ-87 are read and written through these surfaces. `POST`/`DELETE /api/v1/tasks/{id}/edges` serve every type. The body names `type` and exactly one of `to` (the row runs from `{id}`) or `from` (the row runs to `{id}`). `POST /api/v1/tasks` accepts `parent` and `follow_up_to` so a child or follow-up is created with its edge in one transaction. There is no create-time `duplicate_of`. CLI:

| Command | Effect |
|---|---|
| `lode task block <id> --by <id>` / `unblock` | `blocks` row from the `--by` task to `<id>` |
| `lode task blockers <id>` | open blockers, transitively |
| `lode task add --parent <id>` / `--follow-up-to <id>` | create with edge |
| `lode task parent <id> --under <id>` / `unparent <id>` | `child_of` |
| `lode task follow-up <id> --of <id>` / `unfollow-up <id>` | `follow_up_to` |
| `lode task duplicate <id> --of <id>` / `unduplicate <id>` | `duplicate_of` |
| `lode task tree [<id>]`, `lode task list --parent <id>` | read hierarchy |

The task page lists Parent, Children, Follow-up to, Follow-ups, Duplicate of and Duplicates.

