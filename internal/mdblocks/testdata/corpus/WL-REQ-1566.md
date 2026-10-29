
| Route | Body | Response | Permission |
|---|---|---|---|
| `POST /api/v1/projects/{id}/session-usage` | `{agent, external_session_id, by_task}`, all required; `by_task` maps a task id or `""` to `SessionUsageBucket` arrays | 204; 400 malformed; 404 unknown project | `project.report`, granted to every authenticated role |

The route calls the whole-session replacement of WL-REQ-40. There is no overhead-only endpoint, because a per-destination write could count a session twice. `project.report` is separate from `task.claim` because reporting spend is not a claim on a task. Permissions are defined in WL-REQ-49.

