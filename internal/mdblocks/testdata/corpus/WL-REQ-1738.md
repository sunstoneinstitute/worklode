
| Rule | Statement |
|---|---|
| S1 | One comparator, `model.CompareTaskIDs` and `model.SortTaskIDs` in `internal/model`. Task ids sort by key lexically, then by number numerically, so `WL-2` precedes `WL-10`. A project key matches `^[A-Z][A-Z0-9]{1,9}$`, so every id is `<KEY>-<n>` with one separator. |
| S2 | It is the default order for any task sequence the client builds, completion candidates included. |
| S3 | The SQL ordering is pinned to it by a store test over a shuffled id set. |
| S4 | Deliberate rankings are exempt: `task frontier`, `task board`, `task critical-path` use the ranking from WL-SPEC-75#sec-8, and `task list` orders by priority first. Id order is the default and the tiebreak. |


