
`internal/cmd/namerule_test.go` walks the built cobra tree, hidden commands included, and fails on:

1. A top-level command outside the L1, L2, L7, L8 and L9 sets.
2. A verb outside the L3 canonical set and the domain-action allowlist.
3. A hyphenated verb, unless allowlisted. One entry exists: `task follow-up`/`task unfollow-up`, which names the `wl:followUpOf` edge and has no single-word verb.
4. A parent command below the top level with exactly one child. A top-level entity with one subcommand (`actor`, `blob`) is correct as it is.

Checks 2 and 3 need to know which subcommand names are not verbs. This table is the closed set of L6 views and nested entity groups. Adding a member is part of adding the command:

| Parent | Noun subcommands |
|---|---|
| `lode doc` | `progress`, `referrers`, `reviewers`, `sections`, `todo`, `versions` |
| `lode event` | `subscribers` |
| `lode graph` | `drift`, `gaps`, `quarantines`, `triples` |
| `lode project` | `crew`, `focus`, `health`, `overview`, `rally`, `repo` |
| `lode rule` | `versions` |
| `lode secret` | `catalog`, `status` |
| `lode task` | `blockers`, `board`, `brief`, `checklist`, `cost`, `critical-path`, `frontier`, `skills`, `timeline`, `tree` |
| `lode work` | `status` |

Each allowlist entry carries a comment giving its reason. A second test asserts every command whose usage string carries an entity placeholder has a `ValidArgsFunction` (C1). Both follow the tripwire pattern of `renderrule_test.go` and `filerule_test.go`: the test names what it rejects and why.

L4's "verbs are verbs" and L6's "views are nouns" cannot be tested. They are review's job, and `internal/cmd/CLAUDE.md` states the law for review to read.

