
| Act | Who |
|---|---|
| Read documents and rules | `doc.read` |
| Add, edit a draft, open a revision, note, edit a rule or its edges, apply a supersede map | `doc.write` |
| Accept, land a revision | owner |
| Discard a revision | owner or the revision's author |
| Transfer ownership | owner or admin |
| Import with stated status, `lode doc resolve` | `doc.import`, admin |
| Change a task's governing links | `task.write` |
| `event seek`, `event tail --follow` | admin |

Roles and how they map to these permissions are in WL-SPEC-74#sec-3.

