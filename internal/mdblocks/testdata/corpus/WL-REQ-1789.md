
| Route | Guard |
|---|---|
| `GET /projects/{id}/progress` | `permWebRead` |
| `GET /projects/{id}/progress/summary` | `permWebRead` |
| `GET /projects/{id}/progress/spec/{doc}` | `permWebRead` |
| `GET /projects/{id}/progress/events` | `permWebRead` |
| `POST /projects/{id}/progress/accept` | `permDocWrite` |
| `POST /projects/{id}/progress/plan` | `permTaskWrite` |
| `POST /projects/{id}/progress/rally/add` | `permTaskWrite` |
| `POST /projects/{id}/progress/rally/confirm` | `permTaskWrite` |
| `POST /projects/{id}/progress/rally/discard` | `permTaskWrite` |
| `POST /projects/{id}/progress/merge` | `permTaskWrite` |
| `GET /api/v1/projects/{id}/progress` | `permProjectRead` |

