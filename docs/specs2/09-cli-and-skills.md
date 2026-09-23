# CLI and skills

`lode` is the command-line surface of Worklode. Its command tree follows nine naming rules, enforced by a test over the built cobra tree. Every project-aware command resolves its project scope through one chain that ends at the git remote, so a fresh clone is scoped with no configuration. Org-wide agent skills are indexed from git into the backbone, selected server-side by embedding similarity and pins, and delivered to agents as files and brief sections without touching any native skill registry. Design skills from two third-party plugins are vendored into the lode plugin with recorded transformations and a drift check.

## 1. The naming law

Every command is explicable by exactly one of these rules. The law governs spelling. It does not govern flag names, `--json` shapes, or output formatting, and it does not remove capabilities.

| Rule | Statement |
|---|---|
| L1 | Entity commands are `lode <entity> <verb>`. Entity nouns are singular and are exactly what the backbone models: `actor`, `approval`, `blob`, `channel`, `clause`, `decision`, `deliverable`, `doc`, `event`, `gate` (the design authority gate, 11-design-authority-gate.md §3), `graph`, `inbox`, `milestone`, `project`, `secret`, `skill`, `task`, `token`. No bare top-level command acts on an entity. |
| L2 | Bare top-level commands act on this machine or this checkout. The set is closed: `doctor`, `install`, `uninstall`, `login`, `logout`. |
| L3 | One verb per operation. `add` creates, `show` reads one, `list` reads many, `edit` replaces a body, `set <field>` writes one named field or state, `remove` drops a member from a collection, `delete` tombstones an entity. Any other verb names a domain action none of these expresses. The allowlist: `claim`, `release`, `renew`, `submit`, `abandon`, `reopen`, `rework`, `start`, `stop`, `publish`, `promote`, `revoke`, `sync`, `exec`, `purge`, `import`, `install`, `recommend`, `resolve`, `decompose`, `instruct`, `reconcile`, `transfer`, `accept`, `revise`, `lint`, `derive`, `seek`, `tail`, `gc`, `link`, `dismiss`, `serve`, `listen`, `next`, `resume`, `attach`, `detach`, `assign`, `block`, `govern`, `parent`, `duplicate`, `request`, `pack`, `note`, `escalate`, `gap`, `fix`, `withdraw`, `report`, `fetch`, `supersede`, `check`. |
| L4 | Verbs are imperative verbs. No adjectives, no hyphenated verbs except the §4 allowlist. `set` is a verb; the field it writes is an argument. |
| L5 | Inverses take `un-` on the forward verb: `block`/`unblock`, `assign`/`unassign`, `delete`/`undelete`, `install`/`uninstall`. |
| L6 | Named views are nouns, never verbs. A read-only projection over one entity may be a subcommand named for the view. A view never writes. Its paired write is `set <field>`, never a `--set` flag on the view. `project rally` is the one exception: its content is `blocks` edges, written with `task block`. |
| L7 | Cross-entity readers sit at the top level. The set is closed at `show` and `search`. `lode show <ref>` takes a known reference or `--kind` and returns one subject. `lode search <query>` takes an unknown query and returns a ranking over docs, tasks and skills. Every entity keeps its typed `show`. |
| L8 | One workflow group, `lode work`. Commands acting on the task in the worktree the caller stands in live there and nowhere else. |
| L9 | A closed shortcut list of four top-level aliases: `lode next` (`work next`), `lode status` (`work status`), `lode board` (`task board`), `lode overview` (`project overview`). They are permanent API and appear in `lode --help` under their own heading. |

Adding to L2, L7 or L9 requires amending this section. Two commands that differ only in audience are both legitimate domain actions: `task start` (an owner picks up their own task, no lease, no worktree) and `task claim` (an agent takes a lease and a worktree).

`set` records a fact the world produced. Every other transition verb names an act someone takes. `task set state <state> <id>` accepts the states ingestion produces (`merged`, `deployed_dev`, `deployed_prod`, `released`) so a missed webhook can be reconciled by hand. It does not widen the state machine. The server's guards are unchanged and `abandon`, `reopen`, `rework`, `submit`, `start`, `stop` and `publish` keep their own commands and guards. The task state machine is defined in 03-tasks-and-execution.md.

The general form of a field write is `lode <entity> set <field> <value...>`, with the entity named the way that entity is normally named: `<id>` for a task, `--project` scope for a project.

`graph` answers "does the code match the design" (drift, gaps, the derivers). `task` answers "what work next" (`frontier`, `critical-path`). `task reconcile` repairs task state that GitHub ingestion missed. `project health` is the project-scoped check and deliberately differs in name from the machine-scoped `lode doctor`.

## 2. The command tree

Twenty-six top-level commands and four shortcuts. The first table is every command the built binary exposes, grouped by entity. The second is the commands this document designs that are not built.

| Command | Class | Subcommands |
|---|---|---|
| `actor` | L1 | `add` |
| `approval` | L1 | `add`, `list`, `request` |
| `blob` | L1 | `gc` |
| `channel` | L1 | `serve` |
| `clause` | L1 | `edit`, `link`/`unlink` (`--derived-from <ref>` records `wasDerivedFrom`, a split), `set owner`, `set tags`, `supersede --map <file> [--dry-run]` (the refactor primitive, S24); view `versions`. Reading one is `lode show WL-CL-<n> [--version <v>]` |
| `decision` | L1 | `add`, `edit`, `list`, `resolve`, `show` (addressed as `<task>/<key>`) |
| `deliverable` | L1 | `add`, `list`, `report <deliverable> <state>` (ids `<KEY>-DEL-<n>`) |
| `doc` | L1 | `add`, `show`, `list [--status all]`, `edit`, `revise`, `submit`, `accept`, `withdraw`, `note`, `lint`, `import`, `transfer`, `delete`/`undelete`, `set reviewers`; views `progress`, `referrers`, `reviewers`, `sections`, `todo`, `versions` |
| `event` | L1 | `tail [--follow]`, `seek`; view `subscribers` |
| `gate` | L1 | `check` |
| `graph` | L1 | `derive`; views `drift`, `gaps`, `quarantines`, `triples` |
| `inbox` | L1 | `list`, `import`, `link`, `promote`, `dismiss` |
| `milestone` | L1 | `add`, `list`, `delete`, `attach`/`detach` |
| `project` | L1 | `add`, `show`, `list`, `resolve [--refresh]`, `set decision`, `set flow`, `set focus [--clear]`, `set focus-note`, `set settings`; nested groups `crew add/remove`, `repo add/edit/remove`; views `overview`, `health`, `focus`, `rally`, `crew` |
| `secret` | L1 | `exec`, `purge`, `pack` (hidden); views `catalog`, `status` |
| `skill` | L1 | `list`, `install <name>[@<hash>]`, `recommend`, `sync` |
| `task` | L1 | `add`, `show`, `list`, `edit`, `publish`, `set state`, `set skills`, `set checklist`, `claim`, `release`, `renew`, `submit`, `abandon`, `reopen`, `rework`, `start`, `stop`, `block`/`unblock`, `govern [--pin]`/`ungovern`, `assign`/`unassign`, `parent`/`unparent`, `duplicate`/`unduplicate`, `follow-up`/`unfollow-up`, `attach`/`detach`, `delete`/`undelete`, `decompose`, `instruct`, `escalate`, `gap`, `fix`, `reconcile`; views `blockers`, `board`, `brief`, `checklist`, `cost`, `critical-path`, `frontier`, `skills`, `timeline`, `tree` |
| `token` | L1 | `add [--task <id>]`, `revoke` |
| `work` | L8 | `next [--replan]`, `resume`, `submit`, `block`, `status`, `listen` |
| `show`, `search` | L7 | |
| `doctor`, `install`, `uninstall`, `login`, `logout` | L2 | |
| `board`, `next`, `overview`, `status` | L9 | |

Designed, not built:

| Command | Class | Defined in |
|---|---|---|
| `doc fetch <ref>... \| --all` | L1, L3 allowlist verb `fetch` | §8.5 |
| `/lode:assay`, `/lode:planning`, `/lode:writing-for-agents`, `/lode:tdd`, `/lode:debugging`, `/lode:handoff`, `/lode:domain-modeling` | plugin skills | §8.2 |

`project repo` and `project crew` are nested entity groups. `remove` takes a member out of a collection; `delete` tombstones an entity. A rally is a task kind, not an entity, so it is assembled with `task add --kind rally --draft`, `task block <rally> --by <task>` per member, and `task publish <rally>`; `project rally` is its read.

The slash commands and skills in `plugins/claude/lode/` follow the CLI and are named `/lode:<name>`. The agent hooks are described in 08-agent-harness-and-sessions.md.

## 3. Completions and ordering

Completions:

| Rule | Statement |
|---|---|
| C1 | Every positional argument naming an entity registers a `ValidArgsFunction`. One helper per entity kind (task, doc, project, actor) in `internal/cmd/completion.go`. No inline completion bodies. Candidates are scoped to the resolved project (§5). |
| C2 | Completions are fast and silent, or absent. Hard deadline of 250ms on the API call. On any failure (no token, offline, unresolved scope, slow server) return `nil, cobra.ShellCompDirectiveNoFileComp`. Never `ShellCompDirectiveError`, never `cobra.CompErrorln`. |
| C3 | Completions carry a description: `"WL-5\tfix the thing"`, so shells render the title beside the id. |
| C4 | Enumerable flags complete from their value set: `--kind`, `--status`, `--priority` from static sets pinned to `ns` (`--kind` to `ns.TaskKinds`), `--project` from live projects. Field names of `set <field>` complete the same way. |

Ordering:

| Rule | Statement |
|---|---|
| S1 | One comparator, `model.CompareTaskIDs` and `model.SortTaskIDs` in `internal/model`. Task ids sort by key lexically, then by number numerically, so `WL-2` precedes `WL-10`. A project key matches `^[A-Z][A-Z0-9]{1,9}$`, so every id is `<KEY>-<n>` with one separator. |
| S2 | It is the default order for any task sequence the client builds, completion candidates included. |
| S3 | The SQL ordering is pinned to it by a store test over a shuffled id set. |
| S4 | Deliberate rankings are exempt: `task frontier`, `task board`, `task critical-path` use the ranking from 03-tasks-and-execution.md, and `task list` orders by priority first. Id order is the default and the tiebreak. |

## 4. Enforcement

`internal/cmd/namerule_test.go` walks the built cobra tree, hidden commands included, and fails on:

1. A top-level command outside the L1, L2, L7, L8 and L9 sets.
2. A verb outside the L3 canonical set and the domain-action allowlist.
3. A hyphenated verb, unless allowlisted. One entry exists: `task follow-up`/`task unfollow-up`, which names the `wl:followUpOf` edge and has no single-word verb.
4. A parent command below the top level with exactly one child. A top-level entity with one subcommand (`actor`, `blob`) is correct as it is.

Checks 2 and 3 need to know which subcommand names are not verbs. This table is the closed set of L6 views and nested entity groups. Adding a member is part of adding the command:

| Parent | Noun subcommands |
|---|---|
| `lode clause` | `versions` |
| `lode doc` | `progress`, `referrers`, `reviewers`, `sections`, `todo`, `versions` |
| `lode event` | `subscribers` |
| `lode graph` | `drift`, `gaps`, `quarantines`, `triples` |
| `lode project` | `crew`, `focus`, `health`, `overview`, `rally`, `repo` |
| `lode secret` | `catalog`, `status` |
| `lode task` | `blockers`, `board`, `brief`, `checklist`, `cost`, `critical-path`, `frontier`, `skills`, `timeline`, `tree` |
| `lode work` | `status` |

Each allowlist entry carries a comment giving its reason. A second test asserts every command whose usage string carries an entity placeholder has a `ValidArgsFunction` (C1). Both follow the tripwire pattern of `renderrule_test.go` and `filerule_test.go`: the test names what it rejects and why.

`TestAgentSurfaces` resolves every in-tree `lode` invocation against the cobra tree, and `TestCommandReference` fails on a stale command catalog (regenerated with `go test ./internal/cmd -run TestCommandReference -update-command-ref`). A rename therefore has an enumerable blast radius: rename in `internal/cmd`, run `make test`, fix the surfaces it names, regenerate the catalog and the Codex mirror (`./scripts/sync-codex-marketplace.py`), then update `worklode-onboarding` in `sunstoneinstitute/claude-plugins` and bump its `lode-cli-version:` stamp. Two surfaces the test cannot see: `.github/agent-host/poke.sh` hardcodes `lode work listen`, and `supervisor.sh` feeds `plugins/claude/lode/skills/start-agent-loop/SKILL.md` to a model by path. Renames ship without compatibility aliases or a deprecation period. `docs/agent-surfaces.md` is the register of these surfaces.

L4's "verbs are verbs" and L6's "views are nouns" cannot be tested. They are review's job, and `internal/cmd/CLAUDE.md` states the law for review to read.

## 5. cmd decides, cli renders

`internal/cmd` holds the cobra commands. `internal/cli` holds the HTTP client, project scoping and rendering, and never imports cobra, which lets `internal/hookrun` and `internal/worktree` reuse it. Every human-readable view of an `internal/model` shape is a `cli.*Table` or `cli.*Render` function taking an `io.Writer`. A cobra `RunE` fetches, picks `--json` or human, and calls exactly one of them. Nothing under `internal/cmd` builds a `tabwriter` or formats a timestamp; shared cell formatters (`cli.LocalTime`, `cli.Money`, `cli.HumanTokens`, `cli.DocNumber`, `cli.KeySuffix`) are never re-derived. Output over values that never cross the API (a dry run over walked files, the hook name list, no-work guidance) may render in `internal/cmd`. `internal/cmd/renderrule_test.go` catches a hand-built tabwriter or a hand-formatted timestamp. Every wire shape lives once in `internal/model`, as described in 01-system-and-deployment.md.

## 6. Project scoping

The default scope of a project-aware command is the current repo's project. Tasks have no repo column, so there is no repo-level task filter. `--repo owner/name` is sugar for naming a project by one of its repos. A command scopes to one project or to all (`--project=`). There is no multi-select.

### 6.1 Resolution chain

First hit wins:

1. `--project X` or `--repo owner/name` on the command line. An explicitly empty `--project=` means all projects and stops here. `--project` and `--repo` together is an error.
2. `current_project` in the repo-local `.worklode/config.toml`, found by walking up from the working directory.
3. `current_project` in `~/.config/worklode/config.toml`.
4. The git remote: `git remote get-url origin`, resolved against `project_repos` by the server.
5. Nothing resolved: unscoped, all projects.

Step 4 never fails a command. Not a git repo, no `origin`, server unreachable, unmapped repo: each falls through to step 5. A command that cannot proceed unscoped (`task add`) reports the missing project itself. `.worklode/config.toml` may also carry `project_key`, which lets a commit hook know the key without the server or the cache. It is not part of the chain.

### 6.2 Server side

`GET /api/v1/projects/resolve?remote=<url>` returns the project a remote maps to, or 404 when unmapped. It accepts every form `git remote get-url` emits plus a bare `owner/name`. Normalization: strip an scp-style `user@host:` prefix or a URL scheme and authority, drop a leading `/`, drop a trailing `.git` and trailing `/`, then require exactly two non-empty path segments. Anything else is 422. The host is discarded, since `project_repos.repo` is `owner/name` and unique. The response is the same project object `GET /api/v1/projects` returns, so it carries `key`. The route is read-only (plain auth, no admin) and is registered so the literal `resolve` segment wins over `{id}`.

`GET /api/v1/inbox?project=<id>` filters issues by joining `project_repos` on the issue's repo. Empty or absent keeps org-wide behaviour. An unknown project id yields an empty list. `GET /api/v1/board?project=` and `GET /api/v1/tasks?project=` do one-or-all.

### 6.3 Client cache

An `internal/cli` resolver owns steps 2 to 5 and the cache at `~/.cache/worklode/remotes.json`, mode 0600, written atomically. Structure:

| Key | Content |
|---|---|
| `servers.<base URL>` | One section per server, so a local test server's mapping is never served to the team server. Each command reads and writes only its own server's section. |
| `remotes.<raw remote string>` | `{project, at}`. Keyed by the unmodified `git remote get-url origin` output; the server normalizes. An empty `project` is a negative entry, written on a definite 404 or 422. |
| `keys.<project id>` | `{key, at}` for the bare-number shorthand. Filled by a remote lookup or from `GET /api/v1/projects` on first use. |

TTL is 7 days for a hit, 1 hour for a miss. A corrupt, unreadable or unwritable cache is treated as empty and never fails a command. An explicit `--project`/`--repo` bypasses the cache.

`lode project resolve [--refresh]` prints the resolved project and which chain step produced it, for example `worklode (WL) from git remote git@github.com:sunstoneinstitute/worklode.git (cached)`. `--refresh` re-queries and rewrites the entry; it is the supported way to fix a stale mapping. `lode work status` also reports the resolved project and its chain step.

### 6.4 Bare task numbers

One place in the client handles every id-taking command: an argument matching `^[0-9]+$` is prefixed with the resolved project's key, anything else passes through untouched. Full ids work from anywhere, with no cross-project warning or refusal. With no project resolved, a bare number is an error: `12 is a task number, not a task id, and no current project is set: pass a full id like WL-12, or set current_project`.

### 6.5 `lode show` dispatch

`lode show` infers an id's kind from its shape or takes it from a flag. The type segment of a typed id (`<KEY>-<TYPE>-<n>`) is the dispatch key; an id with no type segment is a task. The typed grammar is checked before the task grammar, so a document reference never parses as a task id.

| Argument shape | Routed to |
|---|---|
| `12`, `WL-12` | `task show` (bare numbers per §6.4) |
| `WL-SPEC-25`, optional `#sec-...` | the document renderer (05-documents.md) |
| `WL-PLAN-4-1`, `WL-MILE-2`, `WL-DEL-3` | recognized typed ids |
| any other `<KEY>-<TYPE>-<n>` | an error listing the known type codes |

Explicit flags: `--spec 15`, `--plan 4-1`, `--milestone 2`, `--deliverable 3`, `--task 12`, `--project <id>`, or the generic `--kind <K> <ordinal>` with `K` in `task|spec|plan|milestone|project|deliverable`. Ordinal-taking flags take a bare ordinal, never a typed id. A per-kind flag and a positional id are mutually exclusive.

### 6.6 Command surface for scoping

| Command | Scoping |
|---|---|
| `task list`, `task claim --next`, `work next` | default scope plus remote fallback |
| `task add` | default scope; an error when nothing resolves or `--project=` is explicit |
| `task board` | `--project`, `--repo`, default scope; positional `[project]` is shorthand |
| `inbox list` | `--project`, `--repo`, default scope |
| every id-taking task command, `task timeline`, `task block --on` | accept a bare task number |
| `project list` | org-wide, unscoped |
| `work resume`, `work submit`, `install`, `actor`, `token`, `login` | act on a worktree, an explicit id, or the whole install |

Every command taking `--project` also takes `--repo`, and both carry help text naming the default source. Non-goals: multi-project selection, a task-to-repo association, scoping `project list`, writing `current_project` back into a tracked file, cross-project guardrails on id-taking commands.

## 7. Org-wide agent skills

Skills are Worklode-distributed and Worklode-recommended. Discovery is server-side selection (embeddings plus pins) with deterministic delivery (task brief plus local files). Machinery finds and fetches; the model only judges whether an offered skill is relevant. Skills are never registered in any native skill registry and cost no per-skill description in session context. Any agent that can read a file participates: activation is reading `~/.worklode/skills/<name>/SKILL.md`.

### 7.1 Registry and git sync

Git is the source of truth. Server config lists skill source repos (`owner/repo@ref:glob`, for example `plugins/*/skills/*/SKILL.md`). Authoring and review stay PR-based; Worklode indexes and distributes. Format is the existing SKILL.md convention: frontmatter `name`/`description` plus body, plus optional sibling files (`references/`, scripts).

Sync: webhook push ingests changed skill dirs; `lode skill sync` (admin) triggers a full resync via `POST /api/v1/skills/sync`. A skill removed from git is soft-deleted.

Schema:

| Table | Columns |
|---|---|
| `skills` | `id`, `name` (unique on the qualified name, §7.4), `description`, `source_repo`, `source_path`, `latest_version_id`, `deleted_at` |
| `skill_versions` | `id`, `skill_id`, `git_commit`, `content_hash` (sha256 over the canonicalized skill dir), `frontmatter` jsonb, `skill_md` (body inlined for briefs and embedding), `archive` bytea (tar of the skill dir), `created_at`; unique `(skill_id, content_hash)` |
| `skill_embeddings` | `skill_id`, `chunk_index`, `embedding` vector; latest version only |

Git is the version. Latest on the source ref is what gets recommended and installed; the content hash pins exactness. No semver.

Each skill projects to a `wl:Skill` node (identity only: name, description, source-repo IRI, IRI `wlid:skill/<name>`) so design documents can assert `wl:recommendsSkill` pins and tasks carry `wl:requiresSkill`. The vocabulary is defined in 07-knowledge-graph-and-search.md.

### 7.2 Embeddings and recommendation

The provider interface is `Embed(texts) -> vectors` behind config, default an OpenAI-compatible HTTP endpoint (URL, model, key via env or SOPS). Dimension is fixed per instance. Only the server holds embedding credentials. Description plus SKILL.md body are split into overlapping chunks, one vector per chunk; a skill's score is the max cosine over its chunks. Only SKILL.md is embedded, so SKILL.md must carry the text that should match. Re-embed on content change; a provider or model change invalidates all embeddings and triggers a full re-embed.

`POST /api/v1/skills/recommend` takes `{task_id | text, limit}`. For a task the server assembles the query text from title, description and governing-spec excerpt, embeds it, and keeps the top-k above a server-side score floor. It returns `{pinned: [...], matches: [{name, description, version_hash, score}]}`; pins are never duplicated into matches.

`lode skill recommend [--task <id> | --file <path> | --text <s>] --json` is the one generic surface. `lode task brief` calls it; other stages call the same CLI. Corpus-wide ranking over docs, tasks and skills together is `lode search`, described in 07-knowledge-graph-and-search.md.

### 7.3 Pins, delivery, brief

Task pins are a `skills` name list on the task (`lode task set skills`). Design-doc pins are `skills: [name, ...]` in doc frontmatter, declared as `wl:recommendsSkill` edges on ingest. A brief resolves task pins union governing-design pins. A pin naming an unknown skill is a brief warning, never a failure.

Local layout is a content-addressed store plus name symlink: `~/.worklode/skills/.store/<hash>/` holds the unpacked dir, `~/.worklode/skills/<name>` symlinks into it. Concurrent worktrees hold different versions without clobbering, and a hash match makes re-fetch a no-op. At brief time the hook fetches whatever the brief lists that is missing locally via `GET /api/v1/skills/<name>/archive/<hash>`. No background sync, no full mirror. Manual surface: `lode skill install <name>[@<hash>]`, `lode skill list`; ranking by query is `lode search`.

`lode task brief` carries a `skills` section. `pinned` holds the full SKILL.md body inline (served from the backbone blob, so pins survive a local fetch failure) plus the local path for sibling files. `recommended` holds name, one-line description, score and local path, with the instruction to read `<path>/SKILL.md` if relevant. Pinned bodies count toward the brief's bounded budget; pins alone blowing the budget is surfaced in the brief as a decomposition signal. Brief injection is described in 08-agent-harness-and-sessions.md.

Degradation:

| Condition | Behaviour |
|---|---|
| No embedding provider | Endpoint returns pins, empty matches, `provider: none`. Pins-only mode is fully functional. |
| Provider down | Pins plus a warning. Never blocks a claim. |
| Archive fetch fails | Recommended skills still listed with an install hint; pinned content unaffected. |
| Skill removed from git | Soft-deleted, never recommended; existing pins resolve with a `deprecated` warning. |

### 7.4 Plugin-qualified skill identity

A skill's registry identity is `<plugin>:<name>`, for example `lode:assay`, `superpowers:brainstorming`. `skillhash.ValidName` permits `:`. The qualifier is the `name` in the nearest `.claude-plugin/plugin.json` above the skill directory. It comes from neither the source repo (one repo can hold many plugins) nor SKILL.md frontmatter (a skill could claim another plugin's namespace). A repo of bare skills with no manifest uses the source repo's last path segment.

`skillsync`'s tarball walk collects manifests anywhere in the tree, keyed by directory, and matches each skill dir by longest prefix. The manifest stays out of the archive, so it does not enter `content_hash`. `skills_name_unique` is unique on the qualified name.

`GET /api/v1/skills/{name}` accepts a qualified name. A bare name matching exactly one skill resolves. A bare name matching more than one is an ambiguity error naming the candidates, in the shape `designdoc.AmbiguousRefError` uses. Task pins and a plan's `skills:` key store what was written and resolve through the same rules; they are not rewritten. `Store.ResolveSkillRefs` counts `worklode_skill_name_ambiguous_total`.

## 8. Vendored design skills

Two MIT plugins, `superpowers` and `mattpocock-skills`, cover the work before a task exists: deciding what to build, interrogating the decision, writing it down. Their skills are vendored into `plugins/claude/lode/skills/`, merged where both treat the same job, and edited to know Worklode's documents and the Sunstone Way. Vendoring is chosen because a skill in another plugin cannot be patched, and the skills that matter carry their file layout inside their guidance. The cost is that Worklode owns the merge on every upstream release.

### 8.1 Layout and provenance

Vendored skills are ordinary skills; provenance is metadata. Each carries an `upstream` frontmatter block naming `plugin`, `version` and `path`, and `merged-with` listing every other source folded in. A skill written from scratch has no `upstream` block, which is how the drift check tells the two apart.

Each vendored skill carries its transformation as a prompt in a sibling `UPSTREAM.md`, addressed to an agent holding the upstream sources. Prompts compose in two layers: a plugin-level `plugins/claude/lode/skills/UPSTREAM-<plugin>.md` (path rewrites, house style, the standing instruction that task state belongs to `lode`) followed by the skill-level prompt. Re-vendoring is a re-run of the prompts against the new upstream, reviewed as a diff.

`scripts/check-vendored-skills.py` compares each vendored skill against the upstream revision in its frontmatter and reports what changed upstream. `--check` exits non-zero for CI and pre-commit; default mode reports only. It never rewrites. Upstream sources are read from the local plugin cache; an uncached revision is reported as unverifiable and exits zero. `plugins/claude/lode/skills/LICENSES.md` carries both MIT notices in full and per-skill provenance.

### 8.2 The skill set

Kept as-is: `handoff`, `domain-modeling`. Remixed, one skill per family:

| Remix | Produces | Sources |
|---|---|---|
| `assay` (`/lode:assay`) | specs | `superpowers:brainstorming` + `mattpocock:grilling` + `mattpocock:to-spec` |
| `planning` | plans | `superpowers:writing-plans` + `mattpocock:to-tickets` |
| `writing-for-agents` | skills, instruction files | `superpowers:writing-skills` + `mattpocock:writing-for-agents` |
| `tdd` | | `superpowers:test-driven-development` + `mattpocock:tdd` |
| `debugging` | | `superpowers:systematic-debugging` + `mattpocock:diagnosing-bugs` |

Design and planning are separate skills because a spec is durable and a plan is spent once its tasks are minted (05-documents.md). They share the question format and diverge on what they drive toward. A merge is neither a concatenation nor winner-takes-all; each remix states in its body which behaviour came from where. The merges are designed before they are written: one research document per family, reviewed and accepted, whose output is the prompt set.

Every remix is Worklode-aware (documents are addressable as `WL-SPEC-<n>`, `lode show` renders a section, tasks come from plan acceptance) and Sunstone Way-aware (the seven stages, so a data-science brainstorm reaches for a Topic Intake Brief and a Gate). The set is called the motherlode in `LICENSES.md` and the prompts, never as an invocation.

Not vendored: everything else, including the execution-loop skills (`executing-plans`, `subagent-driven-development`, `using-git-worktrees`, `finishing-a-development-branch`), whose ownership of branches and task state overlaps `lode work next`/`work submit` and `working-under-worklode`. They are neither vendored nor suppressed.

### 8.3 `lode install` owns versions and suppression

`lode install` records which plugin version a repo uses; the `upstream` block records what that version derived from. A repo does not pin upstream plugins.

`lode install` writes `skillOverrides` entries into `.claude/settings.local.json` turning off the upstream skills the vendored set replaces. It does so unconditionally, whether or not either plugin is installed, so a later plugin install does not silently add a second copy of the flow. The list: every `superpowers` skill (all model-invocable, and `using-superpowers` is injected at every `SessionStart`), and the model-invocable `mattpocock-skills` skills the remixes replace: `grilling`, `tdd`, `diagnosing-bugs`, `writing-for-agents`, `code-review`, `codebase-design`, `prototype`, `research`, `resolving-merge-conflicts`, `wizard`. `domain-modeling` is kept and deliberately absent from the list. Writing is idempotent: owned entries are reconciled on every run, unrelated entries preserved. `lode install` reports what it turned off. The rest of what `lode install` manages is in 08-agent-harness-and-sessions.md.

### 8.4 Grilling over Worklode documents

`assay` finds facts itself rather than asking for anything it could look up. Sources in order: `lode show <ref>` for a document or section (`lode show WL-SPEC-25#sec-9`), then `.worklode/cache/docs/` (§8.5) when the backbone is unreachable.

The destination (a spec, a plan, a task body, or a loose idea) is named at invocation, because it changes the interview. Without one, the skill asks before the first round and stops if it cannot get one. When the frontier empties, the settled design tree is written to the destination: a spec or plan destination produces a conforming document (frontmatter, `{#sec-N}` anchors), a task-body destination writes through the CLI, an idea destination writes a file the user names. The skill never accepts a document and never syncs. Document authoring is described in 05-documents.md.

A round asks the whole frontier at once, each question with a recommended answer, rendered as a document and reviewed in `crit`:

| Grilling | Crit |
|---|---|
| One round's frontier | One review round |
| A numbered question | A block the reviewer comments on |
| An answer | An inline comment anchored to that question |
| Accepting the recommendation | Resolving with no comment |
| Frontier empty | Review approved |

The skill writes `.worklode/cache/grilling/<session>/round-<n>.md`, opens `crit`, blocks, reads the answers back, recomputes the frontier, and writes the next round. The session directory is the interview transcript and feeds write-back. Crit is optional: when absent, the round is asked in the terminal, the upstream behaviour.

### 8.5 `lode doc fetch`

`lode doc fetch <ref>...` fetches named documents; `lode doc fetch --all` fetches every document the backbone holds for the project. It is a client-side cache fill and needs only `docs:read`. `fetch` is an L3 allowlist verb: `show` renders a document to stdout, `fetch` stores its current version on disk, and neither of the seven canonical verbs expresses the second. Documents land verbatim, frontmatter included, at `.worklode/cache/docs/<KEY>-<TYPE>-<n>.md` with a sibling `.meta.json` recording version and fetch time. `.worklode/cache/` is gitignored in its entirety.

Refetch is conditional on the object's monotonically increasing `version`, returned by `GET /api/v1/docs/{id}`. The client sends the version it holds; the server answers with the body when it moved and not-modified when it did not. The mechanism is defined on the version field in `internal/model`, so task and plan bodies use the same path. `worklode_doc_fetch_total{result}` counts `modified`, `not_modified`, `not_found`. With no backbone reachable, a stale cache is used rather than discarded.

Degradation: with neither upstream plugin installed (the normal case) vendored skills are self-contained. A user who re-enables a suppressed skill in `settings.local.json` gets both, reported by `lode install` on the next run. With no backbone reachable and an empty cache, grilling proceeds with no document facts and says so.

## Sources

WL-SPEC-61 (the lode command surface), WL-SPEC-19 (repo-scoped CLI commands), WL-SPEC-16 (org-wide agent skills), WL-SPEC-37 (vendored design skills).

## Open questions

- Whether `lode show` dispatches `WL-MILE-<n>` and `WL-DEL-<n>` to `milestone show` and `deliverable show` now that those entities exist.
- Score floor tuning for recommendations, and whether agents call `lode skill recommend --text` mid-session.
- `domain-modeling` is kept, unsuppressed and model-invocable alongside the `writing-for-agents` remix; both can fire on the same trigger.
- Whether the org registry distributes vendored skills, or only the plugin does.
- An eval harness for skill behaviour; a remix claims to beat both originals and nothing measures it.
