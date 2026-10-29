
`claim --next` selects from tasks that are `ready`, unblocked (no open blocker), unleased, live (not deleted) and claimable. `readyCandidates` excludes a task with children and `kind IN ('decision','rally')`:

```sql
AND NOT EXISTS (SELECT 1 FROM task_edges c WHERE c.to_task = t.id AND c.type = 'child_of')
AND t.kind NOT IN ('decision', 'rally')
```

and excludes the `needs-decomposition` label.

