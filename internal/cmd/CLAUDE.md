# internal/cmd

Cobra commands, both server and client sides. This is the surface agent
instructions are written against.

**Changing a command, flag, `--json` shape, config key or hook name?** Follow
the checklist in `docs/agent-surfaces.md` — agent-facing markdown across this
repo and the `sunstoneinstitute/claude-plugins` marketplace hardcodes these
invocations. `go test -trimpath ./internal/cmd -run TestAgentSurfaces` names
the in-tree ones that broke; the doc covers the rest.

What `lode install` writes is not decided here: git hook files are
`internal/githooks`, agent settings files are `internal/harness`. This package
picks the targets, calls one of them, and reports the result.

## Naming

Every command name follows one of nine rules (`WL-SPEC-61` §1 has the
reasoning). L1–L3, L5, L7–L9 are enforced by
`internal/cmd/namerule_test.go`; L4 ("verbs are imperative verbs") and L6
("named views are nouns, never verbs") are enforced by review — a test cannot
tell an adjective from a verb or a view from an action.

- **L1** — Entity commands are `lode <entity> <verb>`. Entity nouns are
  singular and exactly what the backbone models: `actor`, `approval`, `blob`,
  `channel`, `doc`, `event`, `graph`, `inbox`, `project`, `secret`, `skill`,
  `task`, `token`. No bare top-level command may act on an entity.
- **L2** — Bare top-level commands act on this machine or this checkout, not
  on an entity. The set is closed: `doctor`, `install`, `uninstall`, `login`,
  `logout`. Adding one requires amending spec 061 §1.
- **L3** — One verb per operation. `add` creates, `show` reads one, `list`
  reads many, `edit` replaces a body, `set <field>` writes one named field or
  state, `remove` drops a member from a collection, `delete` tombstones an
  entity. Any other verb must name a domain action none of these expresses:
  `claim`, `release`, `renew`, `submit`, `abandon`, `reopen`, `rework`,
  `start`, `stop`, `publish`, `promote`, `revoke`, `sync`, `exec`, `purge`,
  `import`, `install`, `recommend`, `resolve`, `decompose`, `instruct`,
  `reconcile`, `transfer`, `accept`, `revise`, `lint`, `derive`, `seek`,
  `tail`, `gc`, `link`, `dismiss`, `serve`, `listen`, `next`, `resume`,
  `attach`, `detach`, `assign`, `block`, `parent`, `duplicate`, `request`,
  `pack`, `note`.
- **L4** — Verbs are imperative verbs. No adjectives: `task ready` becomes
  `task publish`. No hyphenated verbs, with exceptions named in the spec 061
  §5 allowlist. `set` is a verb like any other: the field it writes is an
  argument, not part of its name.
- **L5** — Inverses take `un-` on the forward verb: `block`/`unblock`,
  `assign`/`unassign`, `delete`/`undelete`, `install`/`uninstall`.
- **L6** — Named views are nouns, never verbs. A read-only projection over one
  entity may be a subcommand named for the view: `task brief`, `task board`,
  `task tree`, `task blockers`, `task cost`, `task frontier`,
  `task critical-path`, `doc todo`, `doc versions`, `doc reviewers`,
  `secret catalog`, `event subscribers`, `graph quarantines`,
  `project overview`, `project health`, `project focus`, `project crew`. A
  view never writes; the paired write is `set <field>` (L3), never a `--set`
  flag on the view.
- **L7** — Cross-entity readers sit at the top level, and there are two.
  `lode show <ref>` dispatches on a known reference or `--kind` and returns
  one subject; `lode search <query>` takes an unknown one and returns a
  ranking over docs, tasks and skills (040 §9). Every entity also keeps its
  typed `show`. Adding a third requires amending spec 061 §1.
- **L8** — One workflow group, `lode work`. Commands acting on the task in
  the worktree the caller is standing in, rather than on a named entity, live
  under `work` and nowhere else.
- **L9** — A closed shortcut list. Exactly four top-level aliases, each
  because it runs many times per session: `lode next` (`work next`),
  `lode status` (`work status`), `lode board` (`task board`), `lode overview`
  (`project overview`). These are permanent API, not compatibility aliases.
  Adding a fifth requires amending spec 061 §1.

The resulting top-level, twenty-one commands and four shortcuts:

| Class | Commands |
|---|---|
| Entities (L1) | `actor`, `approval`, `blob`, `channel`, `doc`, `event`, `graph`, `inbox`, `project`, `secret`, `skill`, `task`, `token` |
| Workflow (L8) | `work` |
| Cross-entity readers (L7) | `show`, `search` |
| Machine (L2) | `doctor`, `install`, `uninstall`, `login`, `logout` |
| Shortcuts (L9) | `board`, `next`, `overview`, `status` |

`lode work` holds `next`, `resume`, `submit`, `block`, `status`, `listen`.

## Checking whether a command still resolves

Auditing a rename means asking what exists. Two obvious ways lie, and both
cost real time (WL-480, WL-482):

- **`lode <path> --help` proves nothing.** cobra returns `flag.ErrHelp` before
  it validates arguments, so a deleted subcommand exits 0 and prints its
  parent's help. A sweep built on `--help` reports every dead command healthy.
- **Running each command bare to see if it errors is not read-only.**
  `lode install`, `uninstall` and `login` do their work when invoked with no
  arguments: a probe loop over them rewrote git hooks and opened a browser.

Use the generated catalog, which is rendered straight off the cobra tree and
touches nothing:

```bash
go test -trimpath ./internal/cmd -run TestCommandReference -update-command-ref
grep -n "lode task publish" plugins/claude/lode/skills/worklode/references/commands.md
```

To check one specific command you believe is gone, the error text is reliable
where `--help` is not — but only reach for this on a command that does not
exist, never as a loop over a list:

```bash
lode task ready 2>&1 | head -1     # unknown command "ready" for "lode task"
```

The same ordering is why a command group needs `RunE`, not just `Args`: cobra
reaches its `!Runnable()` check before `ValidateArgs`, so an `Args` validator
on a parent that does nothing is dead code — cobra ships `completion` that way
itself. `rejectStrayGroupArgs` in `root.go` supplies both; `groupargs_test.go`
is the tripwire, and it is behavioural because a structural check goes vacuous
once that walk runs.
