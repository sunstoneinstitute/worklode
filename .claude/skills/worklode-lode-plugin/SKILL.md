---
name: worklode-lode-plugin
description: Use when working in plugins/ — the /lode:* slash commands, the lode-worker agent, and the plugin marketplace. Triggers: "add a slash command", "a new /lode: command", "the lode plugin", "disable-model-invocation", "plugin marketplace", "marketplace.json", "the codex plugin", "sync-codex-marketplace", "plugin install". Not for the lode Go CLI itself.
---

# The lode plugin

`plugins/claude/lode/` is the agent-facing half of this repo: the `/lode:*`
task pickup surface and the `lode-worker` agent. It lives here so it versions
with the binary it drives — the lifecycle hooks it used to carry now ship with
the CLI (`lode install`).

There is no `commands/` directory: every `/lode:*` entry point is a skill under
`plugins/claude/lode/skills/`. `next`, `resume`, `done`, `block` and `status`
set `disable-model-invocation: true`, so they are reachable only as the slash
commands `/lode:next` and friends; `working-under-worklode` stays
model-invocable — it is the done/block/release judgment loop a worktree session
loads on its own.

## Marketplace

This repo is its own marketplace, named `worklode`:

```
/plugin marketplace add sunstoneinstitute/worklode
/plugin install lode@worklode
```

`.claude/settings.json` enables `lode@worklode` for this repo, but enabling is
not installing: a fresh checkout still needs the install above — and a session
restart after it — before any `/lode:*` command exists.

## The Claude JSON is the source of truth

Edit `.claude-plugin/marketplace.json`; never hand-edit
`.agents/plugins/marketplace.json` or any `.codex-plugin/plugin.json`. Those
are generated:

```bash
./scripts/sync-codex-marketplace.py          # regenerate
./scripts/sync-codex-marketplace.py --check  # what pre-commit and CI run
```

The generator strips the leading Claude surface tag (`[code]`) from Codex
descriptions and adds Codex interface and installation metadata. Schema
validation is deliberately not vendored — it needs the third-party `jsonschema`
package, and `--check` plus `claude plugin validate .` cover the ground without
adding a Python stack to a Go repo.

Markdown under `plugins/` is exempt from the docs-only CI skip — it is input,
not prose.
