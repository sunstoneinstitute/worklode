
```
project ── milestone ──┬── task ── subtask
                       └── deliverable
```

Milestone and deliverable are entities with their own tables. A milestone stores identity, title and ordering; its progress is a query over its tasks and deliverables. A deliverable cannot be claimed, worked or closed. A task references its milestone through nullable `milestone_id`; a task with no milestone is ongoing maintenance, legal everywhere and the norm for engineering projects. A decision attaches to a milestone like any task.

