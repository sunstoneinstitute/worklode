
`set` records a fact the world produced. Every other transition verb names an act someone takes. `task set state <state> <id>` accepts the states ingestion produces (`merged`, `deployed_dev`, `deployed_prod`, `released`) so a missed webhook can be reconciled by hand; closing a task by hand is `lode task set state merged <id>`, and no `close` or `done` verb exists. It does not widen the state machine. The server's guards are unchanged and `abandon`, `reopen`, `rework`, `submit`, `start`, `stop` and `publish` keep their own commands and guards. The task state machine is defined in WL-SPEC-75#sec-3.

The general form of a field write is `lode <entity> set <field> <value...>`, with the entity named the way that entity is normally named: `<id>` for a task, `--project` scope for a project.

