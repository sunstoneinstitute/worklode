---
name: worklode-ci
description: Use when changing CI workflows or asking why a check did or did not run — "CI skipped my PR", "docs-only PR", "can-be-tested label", "the obsidian job did not run", "add a CI check", "the workflow", "paths filter", "www/ deploy", "e2e suite". Covers the docs-only skip and its docs/specs, docs/plans, plugins exemptions, and the subtree-scoped obsidian gate.
---

# CI, workflows, and repo layout

## The docs-only skip

Docs-only PRs (only `*.md`, `docs/`, `www/`) skip CI checks; the
`can-be-tested` label forces a run.

Some markdown is input, not prose, and is **exempt** from that skip — a PR
touching only these still runs CI:

- `docs/specs/`, `docs/plans/` — parsed by `internal/designdoc`; `secfmt` and
  `inlinespec --check` run over them in `_lint.yml`
- `plugins/` — shipped content behind the Codex mirror check
- `CLAUDE.md`, `internal/cmd/CLAUDE.md`, `.claude/skills/`,
  `docs/agent-surfaces.md` — the agent surfaces `TestAgentSurfaces` checks for
  stale `lode` invocations

The skip is a correctness gate, not a review one: nothing CI runs reads prose
for injected instructions, so do not add paths to the exemption on that basis.

## The subtree-scoped job

The `obsidian` job is the one check scoped to a subtree: it runs only when a PR
touches `plugins/obsidian/` or `_obsidian.yml`, decided by a `gate` output rather than
a `paths:` filter, because a reusable workflow cannot take one.

`can-be-tested` does **not** force it — that label authorises CI, it does not
make an untouched subtree worth rebuilding.

## `www/`

`www/` is the static marketing site: own deploy workflow, shares no code with
the Go build.

## `e2e/`

`e2e/` drives the stack through public surfaces only (HTTP API, signed
webhooks, web pages) — never direct store writes. Keep it that way; it exists
to prove the real user path works. Run it with `make test-e2e` (build tag
required, `TEST_POSTGRES_DSN` reachable).
