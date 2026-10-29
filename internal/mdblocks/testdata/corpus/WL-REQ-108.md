
Domain events carry a curie in `type` and JSON-LD in `payload` (`@type: wl:DocumentAccepted`, `prov:atTime`, `prov:wasAssociatedWith`, `wl:subject`, per-type properties). `wl:Event rdfs:subClassOf prov:Activity`, one subclass per type, generated from `ns/` so vocabulary and code cannot drift (WL-SPEC-79 §3). Every payload names its subject: an event about a task carries `task`, an event about a document carries `doc`.

| Event | Source | Payload |
|---|---|---|
| `task.created` | `cli`, `web`, `watcher` | creation input plus minted `task` |
| `task.updated` | `cli`, `watcher` | changed fields plus `task` |
| `task.skills_set`, `task.checklist_set` | `cli` | pinned skills / item id and `checked`, plus `task` |
| `task.decomposed` | `cli` | child titles plus `task` (the parent) |
| `task.assigned`, `task.unassigned`, `task.started`, `task.stopped` | `cli` | `task`, plus assignee or actor |
| `task.done`, `task.abandoned`, `task.reopened` | `cli` | `task` |
| `task.deleted`, `task.undeleted` | `cli` | `task`, `actor`, `justification` |
| `task.edge_added`, `task.edge_removed` | `cli` | `from`, `to`, `type` |
| `lease.claimed`, `lease.rebound` | `cli` | `task`, `actor`, `worktree` |
| `lease.renewed`, `lease.released` | `cli` | `task`, `actor` |
| `lease.expired` | `system` | `task`, `lease` |
| `agent_session.started`, `agent_session.ended` | `cli` | `task`, `actor`, `agent`, `session` |
| `secrets_materialized` | `cli` | `task`, `actor`, `names` |
| `issue.promoted`, `issue.linked` | `cli` | the request plus `task` |
| `merge.local` | `cli` | `repo`, `sha`, `tasks` |
| `doc.owner_changed`, `doc.deleted`, `doc.undeleted` | `cli` | `doc`, `actor`, `request` or `justification` |
| `doc.revision_discarded` | `cli` | `doc`, the discarded candidate body |
| `wl:DocumentSubmitted`, `wl:DocumentAccepted` | `cli` | document, status transition where there is one |
| `task.gap_found`, `fix.started`, `fix.finished` (`outcome`: `resolved`, `substantive`, `escalated`), `doc.patched`, `doc.stale` | `cli`, `watcher` | the escalation ladder (WL-SPEC-77 §10) |
| `crew.member_added`, `crew.member_removed` | `cli`, `web` | project, actor, roles |

Emission is a thin typed helper per event type, checked against generated code at compile time.

