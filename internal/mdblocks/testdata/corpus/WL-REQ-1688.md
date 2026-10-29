
Answering writes through the backbone:

```
POST /api/v1/tasks/{id}/decisions/{key}/decide   {"picked": [...], "notes": "...", "freetext": "...", "value": "yes|no|unsure"}
```

The handler writes `answer`, `decided_by` and `decided_at` in one transaction. On a `decision` task, the answer that leaves no row unanswered moves the task to `merged` in the same transaction. Only a task in `ready`, `in_progress` or `in_review` that is not blocked takes an answer; an unassigned task is assigned to the decider, and a task assigned to someone else is refused. An answered row is never overwritten: the first answer wins and a second is refused. Unanswered questions stay open rows on an open task; deferring one is re-parenting the row to the task that will decide it (`lode decision edit <task>/<key> --task <other>`). The deck keeps a `localStorage` draft of an unsent note; a failed save is shown on the row.

