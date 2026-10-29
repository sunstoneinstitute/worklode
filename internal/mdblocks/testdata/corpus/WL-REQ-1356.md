
A task's governing rules are its `task_governed_by` rows (WL-SPEC-75#sec-4). Each row has one of three writers, named in `source`:

| Source | Writer |
|---|---|
| `plan` | `lode doc accept` on a plan links each task it mints to every rule the plan covers. Closing a plan, when its last task closes or through `lode doc withdraw`, first links each task it minted that has no link at all to the rules the plan covers |
| `manual` | `lode task govern <id> --by <rule> [--pin]` and `lode task ungovern` (`POST`/`DELETE /api/v1/tasks/{id}/governed-by`, `task.write`, a task-scoped token for its own task), and `lode task add --governed-by <rule>` at creation |
| `gate` | the design authority gate reading a `Spec:` trailer on a planless task's change (WL-SPEC-72#sec-4), configured by the `[gate]` table of `.worklode/config.toml` |

Re-accepting a plan links only the tasks that accept mints; earlier tasks keep their links. A `gate` write never changes an existing link. `lode task govern` and `ungovern` record `task.governed` and `task.ungoverned` events. A rule ref carries its project key, so a governing link may name another project's rule.

