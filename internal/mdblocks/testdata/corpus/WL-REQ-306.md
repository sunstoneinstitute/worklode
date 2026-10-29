
Twenty-six top-level commands and four shortcuts. The table is every command the built binary exposes, grouped by entity, plus cobra's own `completion` and `help`. Designed commands that are not built are listed under Designed, not built.

| Command | Class | Subcommands |
|---|---|---|
| `actor` | L1 | `add` |
| `approval` | L1 | `add`, `list`, `request` |
| `blob` | L1 | `gc` |
| `channel` | L1 | `serve` |
| `decision` | L1 | `add`, `edit`, `list`, `resolve`, `show` (addressed as `<task>/<key>`) |
| `deliverable` | L1 | `add`, `list`, `report <deliverable> <state>` (ids `<KEY>-DEL-<n>`) |
| `doc` | L1 | `add`, `show`, `list [--status all]`, `edit`, `revise`, `submit`, `accept`, `withdraw`, `note`, `lint`, `import`, `transfer`, `link`/`unlink`, `resolve`, `delete`/`undelete`, `set reviewers`; views `progress`, `referrers`, `reviewers`, `sections`, `todo`, `versions` |
| `event` | L1 | `tail [--follow]`, `seek`; view `subscribers` |
| `gate` | L1 | `check` |
| `graph` | L1 | `derive`; views `drift [--component <c>] [--acknowledged]`, `gaps`, `quarantines`, `triples` |
| `inbox` | L1 | `list`, `import`, `link`, `promote`, `dismiss` |
| `milestone` | L1 | `add`, `list`, `delete`, `attach`/`detach` |
| `project` | L1 | `add`, `show`, `list`, `resolve [--refresh]`, `set decision`, `set flow`, `set focus [--clear]`, `set focus-note`, `set settings`; nested groups `crew add/remove`, `repo add/edit/remove`; views `overview`, `health`, `focus`, `rally`, `crew` |
| `rule` | L1 | `show`, `list [--doc <ref>] [--status <s>]`, `edit`, `link`/`unlink` (`--derived-from <ref>` records `wasDerivedFrom`, a split), `set owner`, `set tags`, `supersede --map <file> [--dry-run]` (the refactor primitive, WL-SPEC-77 §19.4); view `versions`. `lode show WL-REQ-<n> [--version <v>]` also reads one |
| `secret` | L1 | `exec`, `purge`, `pack` (hidden); views `catalog`, `status` |
| `skill` | L1 | `list`, `install <name>[@<hash>]`, `recommend`, `sync` |
| `task` | L1 | `add`, `show [--status]`, `list`, `edit`, `publish`, `set state`, `set skills`, `set checklist`, `claim`, `release`, `renew`, `submit`, `abandon`, `reopen`, `rework`, `start`, `stop`, `block`/`unblock`, `govern [--pin]`/`ungovern`, `assign`/`unassign`, `parent`/`unparent`, `duplicate`/`unduplicate`, `follow-up`/`unfollow-up`, `attach`/`detach`, `delete`/`undelete`, `decompose`, `instruct`, `escalate`, `gap`, `fix`, `reconcile`; views `blockers`, `board`, `brief`, `checklist`, `cost`, `critical-path`, `frontier`, `skills`, `timeline`, `tree` |
| `token` | L1 | `add [--task <id>]`, `revoke` |
| `work` | L8 | `next [--replan]`, `resume`, `submit`, `block`, `status`, `listen` |
| `show`, `search` | L7 | |
| `doctor`, `install`, `uninstall`, `login`, `logout` | L2 | |
| `board`, `next`, `overview`, `status` | L9 | |

The `graph` views, `task frontier`, `task critical-path` and `project overview` are reads: none of them changes the knowledge graph, whose queries are defined in WL-SPEC-79 §11. `graph drift --acknowledged` adds accepted deviations, active and expired. Every command's `--json` output is deterministic, so it can be compared and grepped.

`project repo` and `project crew` are nested entity groups. `remove` takes a member out of a collection; `delete` tombstones an entity. A rally is a task kind, not an entity, so it is assembled with `task add --kind rally --draft`, `task block <rally> --by <task>` per member, and `task publish <rally>`; `project rally` is its read.

The slash commands and skills in `plugins/claude/lode/` follow the CLI and are named `/lode:<name>` (§7.5). The agent hooks are described in WL-SPEC-80 §4.

