
| Endpoint | Body |
|---|---|
| `POST /api/v1/tasks/{id}/agent-session` | `{agent, agent_version, session_id, usage?}` |
| `POST /api/v1/tasks/{id}/agent-session/end` | `{agent, session_id, input_tokens?, output_tokens?, cost_amount?, cost_currency?, usage?}` |
| `POST /api/v1/projects/{id}/session-usage` | replacement session totals from Edge Agent |

`cost_amount` crosses the wire as a decimal string and is validated in Go against the column shape. The surface is named `agent-session`, never `session`, which `internal/api/session.go` owns for web and CLI auth.

