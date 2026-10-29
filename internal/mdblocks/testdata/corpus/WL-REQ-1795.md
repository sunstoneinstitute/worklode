
`GET /api/v1/crew` lists every human actor on the instance as a `CrewMember` (`actor`, `display_name`), sorted by display name. It carries no project roles and no `lead` flag. Pickers that choose a person (a task's assignee, a document's participants) read this list and show the member for an id; a project's own roster (`GET /api/v1/projects/{id}/participants`, WL-REQ-343) is offered first where the picker sits inside a project, and this list when that roster is empty. The route is guarded by `permProjectRead` and accepts a bearer token or the session cookie (WL-REQ-1307).

Rationale: the SPA refers to people by actor id (WL-RULE-46) and needs one place to turn an id into a name without a project context, for example the cross-project task list.
