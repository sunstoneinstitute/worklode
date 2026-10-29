
The tools version 1 offers, and the route each one calls.

| Area | Tool | Backing route |
|----|----|----|
| project | `project_list` | `GET /projects` |
|  | `project_resolve(spoken)` | `GET /projects` + matcher |
|  | `project_use(project, confirmed?, from_instructions?)` | `GET /projects` + a signed handle |
| find | `search(query, kinds?)` | `GET /search` |
|  | `show(ref, inline=true, section?)` | docs, rules, tasks, projects |
| file | `file(what, title, body?, parent?, doc?)` | one of the three add routes |
| tasks | `task_list(kind?, state?, assignee?)` | `GET /tasks` |
|  | `task_add(kind, title, body, parent?, blocked_by?, governed_by?)` | `POST /tasks` + edges |
|  | `task_update(id, title?, body?, priority?)` | `PATCH /tasks/{id}` |
|  | `task_set_state(id, state, reason?)` | `/state`, `/abandon`, `/reopen` |
| docs | `doc_list(kind?, status?)` | `GET /docs` |
|  | `doc_add(kind, title, body, covers?)` | `POST /docs` |
|  | `doc_edit(id, body or patch, if_version)` | `PUT /body`, `POST /patch` |
|  | `doc_transition(id, submit|accept|revise|withdraw)` | lifecycle routes |
| rules | `rule_list(doc?)` | `GET /rules` |
|  | `rule_add`, `rule_edit` | `POST`, `PATCH /rules` |
|  | `rule_link(id, rel, target, remove?)` | `/rules/{id}/edges` |
|  | `rule_supersede` | `/projects/{id}/rules/supersede` |
| reviews | `doc_reviewers(id, reviewers?)` | `GET`, `POST /docs/{id}/reviewers` |
|  | `approval_request(doc)` | `POST /docs/{id}/request-approval` |
|  | `approval_list(state?)` | `GET /approvals` |
|  | `approval_add(entity, approver)` | `POST /approvals` |
|  | `approval_decide(id, verdict?, note?, confirmed?)` | `POST /approvals/{id}/decide` (new, WL-REQ-1608 v2) |
| decisions | `decision_list(task)`, `decision_show(task, key)` | `GET /tasks/{id}/decisions[/{key}]` |
|  | `decision_add(task, question, options?)` | `POST /tasks/{id}/decisions` |
|  | `decision_resolve(task, key, answer?, confirmed?)` | `POST /tasks/{id}/decisions/{key}/decide` |

Every tool that acts inside a project also takes `project`, the handle `project_use` returned (WL-REQ-1828); the table leaves it out. A review task is filed with `file(what: "review")`, and a document goes to review with `doc_transition(submit)`.

Every tool result starts with one sentence that reads well aloud, then the detail. A list result holds at most 10 items and a cursor for the next page. A cursor the server did not issue answers `invalid`.

A tool that panics returns an error result. The server keeps serving.
