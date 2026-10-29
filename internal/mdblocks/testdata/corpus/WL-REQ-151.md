
| Surface | Behaviour |
|---|---|
| `GET /api/v1/projects/{id}/workflows` | The stored object, or the implied built-in default when NULL. |
| `PUT /api/v1/projects/{id}/workflows` | Full-object replace, validated, evented. Guarded by `workflow.write`, granted to `user` and `admin`. |
| `lode project workflow [--json]` | Read. |
| `lode project set workflow --file <json>` (`-` for stdin) | Write. |
| `lode project set workflow --file <json> --dry-run` | Client-side validation against the same rules, no write. |
| `lode task edit --workflow <name>` | Per-task override. |

Every accepted PUT appends a `project.workflows_set` event carrying the full new object and the actor. **The change is live when the PUT commits.** Rejecting a change is another evented PUT.

