
| Rule | Statement |
|---|---|
| C1 | Every positional argument naming an entity registers a `ValidArgsFunction`. One helper per entity kind (task, doc, project, actor) in `internal/cmd/completion.go`. No inline completion bodies. Candidates are scoped to the project WL-REQ-311 resolves. |
| C2 | Completions are fast and silent, or absent. Hard deadline of 250ms on the API call. On any failure (no token, offline, unresolved scope, slow server) return `nil, cobra.ShellCompDirectiveNoFileComp`. Never `ShellCompDirectiveError`, never `cobra.CompErrorln`. |
| C3 | Completions carry a description: `"WL-5\tfix the thing"`, so shells render the title beside the id. |
| C4 | Enumerable flags complete from their value set: `--kind`, `--status`, `--priority` from static sets pinned to `ns` (`--kind` to `ns.TaskKinds`), `--project` from live projects. Field names of `set <field>` complete the same way. |

