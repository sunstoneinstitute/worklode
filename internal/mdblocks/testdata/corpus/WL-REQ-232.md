
`lode blob gc` asks the server to run two sweeps, each with a grace period (24 hours by default, `--grace-hours`). GC runs only when invoked.

- Unreferenced blobs: a `blobs` row with no `task_blobs` row sharing its hash and older than the grace period. The row is deleted first, inside a transaction that re-checks the zero-reference condition, then the object.
- Orphan objects: any key under `blobs/` whose hash has no `blobs` row and whose `LastModified` is older than the grace period. The sweep lists serially; the two-character shard allows a 256-way split.

Reporting is the default and `--apply` deletes. Deleting a task cascades its `task_blobs` rows.

