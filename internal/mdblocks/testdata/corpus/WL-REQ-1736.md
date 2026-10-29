
`lode inbox link <repo> <number> <task-id>` records that an issue is already covered by a task. `store.LinkIssue` requires `triage_state = 'new'` (else `ErrBadTransition`) and an existing task (else `ErrNotFound`), then sets `triage_state = 'promoted'` and `task_id`. `POST /api/v1/inbox/link`, event type `issue.linked`.

