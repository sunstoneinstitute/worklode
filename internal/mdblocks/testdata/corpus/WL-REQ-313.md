
An `internal/cli` resolver owns steps 2 to 5 and the cache at `~/.cache/worklode/remotes.json`, mode 0600, written atomically. Structure:

| Key | Content |
|---|---|
| `servers.<base URL>` | One section per server, so a local test server's mapping is never served to the team server. Each command reads and writes only its own server's section. |
| `remotes.<raw remote string>` | `{project, at}`. Keyed by the unmodified `git remote get-url origin` output; the server normalizes. An empty `project` is a negative entry, written on a definite 404 or 422. |
| `keys.<project id>` | `{key, at}` for the bare-number shorthand. Filled by a remote lookup or from `GET /api/v1/projects` on first use. |

TTL is 7 days for a hit, 1 hour for a miss. A corrupt, unreadable or unwritable cache is treated as empty and never fails a command. An explicit `--project`/`--repo` bypasses the cache.

