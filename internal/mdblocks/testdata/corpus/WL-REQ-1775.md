
| Command | Does |
|---|---|
| `lode review add <task\|doc>` | open |
| `lode review list [--mine] [--open]` | list |
| `lode review show <id> [--json]` | threads and guide |
| `lode review guide <id> [--focused]` | the current guide; `--focused` shows it with skims applied |
| `lode review set guide <id> [--file <guide.json>]` | author: propose the guide for the current round, with the diff attached |
| `lode review exec <id>` | launch crit with hooks |
| `lode review comment <id> --anchor <a> --intent question\|change_request\|note --body` | open a thread |
| `lode review comment <id> --quote "<text>" --intent question\|change_request\|note --body` | open a thread anchored to that exact quoted text of the subject's stored Markdown; the server finds the byte range (WL-REQ-1317) |
| `lode review comment <id> --thread <tid> --body` | reply |
| `lode review resolve <thread>` | resolve |
| `lode review approve <id> [--focused]` | reviewer round with verdict `approved` |
| `lode review set verdict changes_requested <id> [--focused]` | reviewer round with verdict `changes_requested` |
| `lode review submit <id>` | author round: a document's current version, or a change's HEAD sha with threads re-anchored |
| `lode review wait <id> [--timeout <s>] [--after <seq>]` | author side; long-polls the review's events after sequence number `--after`, so a caller resumes where it stopped; blocks, prints one tagged JSON envelope, exits; `--timeout` prints nothing and exits 0; non-zero means no open review |
| `lode review import --review <id> < result.json` | crit's `on_finish_*` hook target; crit's finish payload carries no review id, so the bridge passes the review explicitly |
| `lode task set reviewers <actor…> <id>` | mirrors `lode doc set reviewers` |

**Clients.** Human at a terminal desk: crit plus `lode review exec`, the
primary human surface. Human at a terminal without a desk: `comment`,
`approve`, `set verdict`. Reviewer agent: `show --json`, `comment`, `approve`
or `set verdict`, with the guide as brief. Author agent: `wait`,
`comment --thread`, `submit`, `set guide`, `rounds/{n}`. Web UI: a read-only
review view that produces no rounds, behind the OIDC gate.
