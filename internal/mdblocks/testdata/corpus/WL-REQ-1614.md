
The API carries the stored declarations of WL-REQ-133:

| Surface | Behaviour |
|---|---|
| `PATCH /api/v1/tasks/{id}` | `deliverables *[]string`: null means inherit, `[]` means explicit empty, a list is an override |
| `PATCH /api/v1/projects/{id}` | `default_deliverables` |
| `GET /api/v1/tasks/{id}/brief` | `deliverables` array and a populated `definition_of_done` (WL-REQ-134) |

