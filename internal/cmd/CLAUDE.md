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
reasoning). L1–L3, L5, L7–L9 are enforced by `internal/cmd/namerule_test.go`
(arriving in plan part 4); L4 ("verbs are imperative verbs") and L6 ("named
views are nouns, never verbs") are enforced by review — a test cannot tell an
adjective from a verb or a view from an action.

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
  `attach`, `detach`, `assign`, `block`, `parent`, `duplicate`.
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
- **L7** — One polymorphic reader. `lode show <ref>` dispatches on the
  reference or `--kind` and is the only cross-entity command. Every entity
  also keeps its typed `show`.
- **L8** — One workflow group, `lode work`. Commands acting on the task in
  the worktree the caller is standing in, rather than on a named entity, live
  under `work` and nowhere else.
- **L9** — A closed shortcut list. Exactly four top-level aliases, each
  because it runs many times per session: `lode next` (`work next`),
  `lode status` (`work status`), `lode board` (`task board`), `lode overview`
  (`project overview`). These are permanent API, not compatibility aliases.
  Adding a fifth requires amending spec 061 §1.

The resulting top-level, twenty commands and four shortcuts:

| Class | Commands |
|---|---|
| Entities (L1) | `actor`, `approval`, `blob`, `channel`, `doc`, `event`, `graph`, `inbox`, `project`, `secret`, `skill`, `task`, `token` |
| Workflow (L8) | `work` |
| Polymorphic reader (L7) | `show` |
| Machine (L2) | `doctor`, `install`, `uninstall`, `login`, `logout` |
| Shortcuts (L9) | `board`, `next`, `overview`, `status` |

`lode work` holds `next`, `resume`, `submit`, `block`, `status`, `listen`.
