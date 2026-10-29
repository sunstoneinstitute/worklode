
`(*Store) ReportProjectSessionUsage(ctx, projectID, agent, externalSessionID string, byTask map[string][]SessionUsageBucket) error` replaces one session's complete usage across a project in one transaction. Task keys land on their `agent_sessions` row through `applySessionUsageTx`. The `""` key lands on `project_overhead_usage`. Both rollups are rebuilt for every affected day. Rules:

- `agent` is validated against the same vocabulary `TouchAgentSession` uses, and an empty `externalSessionID` is rejected. Both are `ErrInvalidInput`. An unknown project is `ErrNotFound`.
- No lease-holder check. The write reaches rows whose lease is gone, and any authenticated actor may report for any project it can name.
- A task named in `byTask` with no `agent_sessions` row bills to overhead.
- A task that had a row and is absent from `byTask` has its usage cleared.

Whole-session scope is what makes a double count impossible to represent. A turn's destination can change between two reports (a directory resolves to a task while the lease is held and to overhead after). Replacing per destination would leave the vacated side holding its copy.

