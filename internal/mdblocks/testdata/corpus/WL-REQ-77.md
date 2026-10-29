
A project is the scoping unit for tasks, focus, repos and (for research work) milestones and crew. Every project has a **key**: required at creation, unique, uppercase, immutable, matching `^[A-Z][A-Z0-9]{1,9}$`. `SPEC` and `ADR` are rejected as keys because they are the type token in the `<KEY>-<TYPE>-<n>` document shorthand (WL-REQ-168). The key is immutable because it is baked into task ids, branch names and `Worklode-Task:` trailers.

Task ids are `<KEY>-<n>` (`WL-12`, `SW-1`). Each project carries `next_task_num`, and `CreateTask` allocates the id inside its own transaction:

```sql
UPDATE projects SET next_task_num = next_task_num + 1
WHERE id = $1 RETURNING key, next_task_num - 1
```

There is no global counter. Every parser that recognises a task id matches `[A-Z][A-Z0-9]*-\d+`. `numericTaskID` parses the digits after the last `-`.

Surface: `store.CreateProject(ctx, id, name, key)`; `POST /api/v1/projects` with `key` required, a unique violation answered as 400; `lode project add <id> --name … --key WL`; `lode project list` shows a `KEY` column.

