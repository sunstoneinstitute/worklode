---
name: worklode-obsidian-mirror
description: Use when working in obsidian/ — the TypeScript Obsidian plugin that mirrors worklode into a vault. Triggers: "the obsidian plugin", "the vault", "sync notes", "wire types", "writeBack", "conflict note", "pnpm install", "build the plugin", or any change to a json-tagged internal/model shape that obsidian/src/api/types.ts hand-mirrors. Not for the Go binary or the web cockpit.
---

# The Obsidian mirror

`obsidian/` is a top-level TypeScript Obsidian plugin, built and shipped
independently of the Go binary, that mirrors a Worklode instance's projects,
docs, and tasks into a machine-owned vault folder.

It is a client of the public `/api/v1` HTTP API only — no store or server
access. Read-only but for one opt-in return path (`writeBack`, default off): a
task note's edited body, pushed with `PATCH /api/v1/tasks/{id}` on a full sync.
The backbone wins any conflict and the local text is preserved as a conflict
note.

Its wire types (`obsidian/src/api/types.ts`) are hand-kept against
`internal/model`, now the one declaration they mirror (ADR 036); generating
them from it instead of hand-mirroring is WL-76, not yet done. If you change a
json-tagged shape in `internal/model`, check whether this file mirrors it.

## Toolchain

The repo's only Node package. It uses **pnpm**, pinned by `package.json`'s
`packageManager` and supplied by corepack — `npm` here will fight the lockfile:

```bash
corepack enable pnpm                            # once per machine
pnpm -C obsidian install --frozen-lockfile
pnpm -C obsidian test                           # also: typecheck, build
```

CI runs the `obsidian` job only when a PR touches `obsidian/` or
`_obsidian.yml` — see `references/ci-and-layout.md`.
