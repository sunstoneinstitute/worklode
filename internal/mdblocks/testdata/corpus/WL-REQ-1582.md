
| Command | Purpose |
|---|---|
| `lode secret catalog` | List catalog names and descriptions (authenticated) |
| `lode secret status` | Declared versus materialized names for the bound task; for templated entries, credentials and whether a rendered file exists |
| `lode secret pack` | Internal child of `op run` at ceremony time |
| `lode secret purge [--task <id>]` | Remove a task's keystore items and rendered files; when bound to a worktree, also remove `.worklode/secrets/` |
| `lode secret exec -- <cmd>` | Run a command with the task's secrets injected |

