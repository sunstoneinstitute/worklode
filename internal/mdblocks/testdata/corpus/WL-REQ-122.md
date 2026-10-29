
Every entity kind draws from its own per-project sequence, from `(project, kind)` counter rows:

| Entity | Form |
|---|---|
| Task, subtask | `COW-7` |
| Milestone | `COW-MILE-2` |
| Deliverable | `COW-DEL-3` |
| Spec, plan | `COW-SPEC-4`, `COW-PLAN-7` |

Numbers are unique per kind. Only tasks are claimable, so only bare `<KEY>-<n>` ids appear in branches, trailers and merge correlation. `lode show <id>` dispatches on the type segment, with an equivalent `--kind` spelling.

