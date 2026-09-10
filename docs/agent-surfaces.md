# Agent surfaces: keeping instructions in sync with the CLI

An *agent surface* is any file an agent loads as instructions rather than as
prose. Most of them hardcode `lode` invocations, so a renamed command or a
dropped flag rots them silently — the CLI still builds, the tests still pass,
and the next agent follows instructions that no longer work.

This is the register of those surfaces, the checklist for when the CLI changes,
and the rules for adding and retiring skills.

## The register

| Surface | Path | Audience | Generated |
|---|---|---|---|
| Root instructions | `CLAUDE.md` (`AGENTS.md` is a symlink to it) | every agent working in this repo | no |
| Worklode entry instructions | `.worklode/agent-instructions.md`, read through root `CLAUDE.md` and `AGENTS.md` pointers | every supported harness | managed block refreshed by `lode install` |
| Command-package pointer | `internal/cmd/CLAUDE.md` | agents editing the CLI | no |
| Marketing site accuracy guide | `www/CLAUDE.md` (`www/AGENTS.md` is a symlink to it) | agents editing `www/` | no |
| Repo-development skills | `.claude/skills/*/SKILL.md` | agents changing this repo | no |
| Path-scoped rules | `.claude/rules/*.md` | agents editing the files each rule's `paths:` glob names | no |
| Shipped plugin, Claude | `plugins/claude/lode/skills/*/SKILL.md`, `plugins/claude/lode/agents/`, `.claude-plugin/marketplace.json` | `lode` users on Claude Code | no — **source of truth** |
| Shipped plugin, Codex | `.agents/plugins/marketplace.json`, `plugins/claude/lode/.codex-plugin/plugin.json` | `lode` users on Codex | yes — `scripts/sync-codex-marketplace.py` |
| `worklode` orientation skill's command catalog | `plugins/claude/lode/skills/worklode/references/commands.md` | `lode` users, on demand | yes — `go test ./internal/cmd -run TestCommandReference -update-command-ref` |
| Org onboarding | `sunstoneinstitute/claude-plugins` → `plugins/sunstone-dev/skills/worklode-onboarding/SKILL.md` | any Sunstone repo adopting Worklode | no — **and out of this tree** |
| Agent host scripts | `.github/agent-host/supervisor.sh`, `.github/agent-host/poke.sh` | the unattended sessions on hel01 | no |

Three of these need care beyond ordinary editing.

**The Codex mirror is generated.** Edit the Claude JSON; `sync-codex-marketplace.py`
regenerates the rest, and `--check` runs in pre-commit and in `_lint.yml`. Details
in the `worklode-lode-plugin` skill.

**The command catalog is generated, straight off the cobra tree.**
`TestCommandReference` (`internal/cmd/commandref_test.go`) re-renders it from
`rootCmd` and fails the diff if the checked-in file disagrees, so it runs
under `make test` on any PR that touches a command or a flag — unlike
`TestAgentSurfaces`, which only catches an invocation that stopped resolving,
this one also catches a command that went missing from the catalog. Add a
command or a flag, then `go test ./internal/cmd -run TestCommandReference
-update-command-ref` before committing.

**The agent host scripts are instructions by another route.** `supervisor.sh`
does not merely invoke `lode`: it reads
`plugins/claude/lode/skills/start-agent-loop/SKILL.md`, strips its frontmatter,
and hands the body to a model as its opening prompt. Moving or renaming that
skill, or changing the `$ARGUMENTS` placeholder it substitutes, breaks the
agent host silently — the session still starts, it just has nothing to do.
`poke.sh` hardcodes `lode work listen` and the filter vocabulary it shares
with the skill (`--project`, `--kind`, `--strict-focus`); a flag that leaves one
side has to leave the other. `TestAgentSurfaces` sees the `lode` invocations in
both, but not the skill path, so that one is on you.

**The onboarding skill lives in another repository.** It walks a repo through
`lode login`, `lode project`, `.worklode/config.toml` and `lode install`, so it
breaks on exactly the changes this repo makes and nothing here can see it. Its
frontmatter carries a `lode-cli-version:` stamp naming the CLI release it was
last checked against; bump the stamp whenever you touch it.

`lode install` writes lifecycle bindings with the sibling binaries
`lode-hook <event>` and `lode-statusline`. The old subcommand spellings were
removed with the WL-319 shim cleanup; `lode install` still recognises them in
existing settings files so upgrades replace pre-split bindings.

`lode install` writes the shared Worklode block to
`.worklode/agent-instructions.md` and adds a plain-language read instruction
to root `AGENTS.md` and `CLAUDE.md` when missing. Symlinked root files share
one pointer. Commit the shared file and the root pointers together so clones
and new worktrees inherit them. Installation from a linked worktree updates
the main checkout; existing worktrees receive these tracked changes through
their normal branch updates.

Upgrades remove legacy Worklode blocks from the root instruction files and
`CLAUDE.local.md`, preserving personal prose. An import-only `CLAUDE.local.md`
created by the old installer is removed. Uninstall strips the shared managed
block, preserves any other prose, and leaves the conditional root pointers.
The install JSON's `instructions.shared_md` reports the shared block action;
`agents_md` and `claude_md` report pointers in `AGENTS.md` and `CLAUDE.md`.

## When the CLI changes

Adding, renaming or removing a command; changing a flag; changing a `--json`
shape; changing a config key, env var, or hook name — any of these:

1. **Run the drift test.** `go test -trimpath ./internal/cmd -run TestAgentSurfaces`
   names every stale invocation and the file and line it sits on. It runs under
   `make test`, so CI catches it on any PR that touches Go.
2. **Fix the in-tree surfaces it names.** Prose is not enough: the surrounding
   explanation usually rots with the invocation.
3. **Regenerate the command catalog** if a command or a flag changed:
   `go test ./internal/cmd -run TestCommandReference -update-command-ref` (also
   under `make test`, so a forgotten regen fails CI on its own).
4. **Regenerate the Codex mirror** if any manifest text changed:
   `./scripts/sync-codex-marketplace.py`.
5. **Update the downstream surface.** If the change touches onboarding —
   `lode login`, `lode project`, `lode install`, `.worklode/config.toml`, the
   lifecycle hooks — update `worklode-onboarding` in
   `~/git/sunstone/claude-plugins` and bump its `lode-cli-version:` stamp. The
   drift test prints this reminder on failure because it cannot check it.
6. **Ask the staleness question** below.

### What the drift test does not cover

It resolves command paths and long flags against the cobra tree, and checks
`--kind` values against the set that command's usage string names — tied to
`ns.TaskKinds` by a test, so the check cannot follow a usage string that has
itself drifted. `--kind` alone gets the value treatment because it is the flag
agent docs get wrong: a task kind is not a document kind, and `spec` is a
retired task-kind spelling the server still accepts as a deprecated alias, but
agent docs must not use it.

The two claim surfaces — `lode work next` and `lode task claim --next` — take
a comma-separated list of kinds (025 §8.8), and their usage names only the six
a ranked pick can hand out, since a decision and a rally are never in the ready
set. The drift test splits a list value and checks each element, so
`--kind design,spike,review` in a skill is checked the same way a single kind
is.

It says nothing about whether the surrounding explanation is still true, whether
a `--json` field an agent parses still exists, or what a command now does
differently. It also does not read the design corpus, which lives in the
backbone rather than this tree (055), for the reason below.

### Specs and plans are not corrected for a rename

A rename leaves old spellings behind in the corpus. **They stay.** A spec states
what was decided; a spent plan records what someone did. Rewriting either to use
today's spelling makes it describe a decision that was not taken or an execution
that did not happen, and the git history that would have shown the substitution
is one more diff to read past. Spec 061 §2 states the same rule from the other
end: command names appearing incidentally in a spec are illustration, not
specification.

What a rename does owe the corpus is a **pointer**, and only where a section's
subject *is* the command surface — a table of spellings, a "command surface"
heading. That is an ordinary amendment (see the `lode:writing-docs`
skill): the
inline `> **Amended by spec NNN.**` note next to the heading, `amends` on the
renaming spec, `amendedBy` on the renamed one. A section that merely mentions a
command in passing gets nothing.

The current surface is `lode --help`, the generated command catalog, and 061
§2.5. No spec is a substitute for any of the three.

An invocation that is deliberately unresolvable — documenting a command before
it ships — goes in `internal/cmd/testdata/agent-surface-exempt.txt` with a
comment saying why and when it comes out.

## Skill lifecycle

Two populations, with different blast radius. `.claude/skills/` is internal to
this repo; `plugins/claude/lode/skills/` ships to every `lode` user.

### Rule or skill

Guidance whose subject is a set of files is a rule in `.claude/rules/`: a
`paths:` glob loads it whenever an agent touches a matching file, so it does
not depend on a `description` matching the prompt. Guidance that spans files,
or that has no path to key on, stays a skill. A rule's cost is that it loads
in full on any match, so keep it to what an agent editing those files has to
know. A rule needs no `CLAUDE.md` bullet of its own; the section's list of
rule filenames is the index.

### Adding a skill

Add one when a topic has recurring triggers *and* a body that would otherwise
bloat `CLAUDE.md`. One skill per subsystem. The `description` decides whether
the skill ever fires, so spell out the concrete phrases and paths that should
trigger it, and name the neighbouring skill it is not. A new
`.claude/skills/` entry also gets a bullet in `CLAUDE.md`'s "Where the rest of
the guidance lives" in the same commit.

### Retiring a skill

- **`.claude/skills/`** — delete it. Git history is the tombstone. Remove its
  `CLAUDE.md` bullet in the same commit, or the pointer outlives the file.
- **`plugins/claude/lode/skills/`** — a skill with `disable-model-invocation:
  true` is a `/lode:*` slash command that users have in their fingers. Leave it
  one release as a stub naming its replacement, then delete it. Model-invocable
  skills carry no muscle memory and can go straight away.

### The staleness question

Step 6 of the checklist: *does any skill now exist only to explain something the
CLI no longer does?* A skill whose trigger phrases all name removed surface is
dead weight that still competes for the model's attention. Retire it rather than
patching around it.
