# web/app

Worklode's web app: a React single-page app that `lode-server` embeds and
serves on its public host (WL-REQ-1303, WL-REQ-1304). Vite + React 19 +
TypeScript, Tailwind CSS v4, shadcn/ui. See `AGENTS.md` for how the code is
organized.

This tree holds only pages the server serves; today that is `/projects`.
Page-specific code from the `worklode-design` prototype (ideas, intake,
documents, crew, the project summary, its optimistic list helpers) stays in
that repo and comes in when its page is ported. That repo's `docs/API.md`
is the prototype's mock contract. The server's contract is
`GET /api/v1/openapi.json`.

## Prerequisites

- Node.js (tested with 26)
- pnpm 12.6.0, the version pinned in `package.json`'s `packageManager` field

If `pnpm` prints `Failed to switch pnpm to v12.6.0 ... ENOEXEC`, your global
pnpm is too old (10.x) to install pnpm 12 correctly. Upgrade it with
`brew upgrade pnpm` (or `npm i -g pnpm@12.6.0`), or run commands through
`npx pnpm@12.6.0 <command>`.

## Build it into the server

From the repo root:

```sh
make web              # pnpm install + pnpm build into internal/webapp/dist
make bin/lode-server  # the server binary embeds that build
make web-clean        # put the committed placeholder back before committing
```

Without `make web`, the binary embeds a placeholder page that says the web
app was not built. `go test` serves that placeholder.

## Regenerate the API types

```sh
make web-gen
```

This writes the server's OpenAPI document to `bin/openapi.json` and
regenerates `src/api/schema.d.ts` from it. Commit the result and never
edit the file by hand. CI fails when it is stale (WL-REQ-1309).

## Run it in development

```sh
pnpm install
pnpm dev
```

Open http://localhost:5173. By default `pnpm dev` answers `/api/v1` with
the MSW mocks, so no server is needed.

To use a local Go server instead, copy `.env.example` to `.env.local` and
set `VITE_API_MOCKS=off`, or run `VITE_API_MOCKS=off pnpm dev`. Vite then
serves the paths in `src/routes.json` itself and proxies everything else
(`/api` and the templ cockpit pages) to `VITE_API_TARGET` (default
`http://localhost:8080`). From the repo root, `make dev-db dev-server` starts
that server and `make dev-web` starts Vite this way (see the root README's
"Server on the host").

## Other commands

| Command          | What it does                                             |
| ---------------- | -------------------------------------------------------- |
| `pnpm build`     | Type-check, then build to `../../internal/webapp/dist`   |
| `pnpm preview`   | Serve that build                                         |
| `pnpm test`      | Run the Vitest suite once (`pnpm test:watch` to watch)   |
| `pnpm typecheck` | Type-check only                                          |
| `pnpm lint`      | Run Oxlint                                               |
| `pnpm iconfont`  | Rebuild the icon font from `src/assets/icons/*.svg`      |
