# Worklode design

The Worklode web UI: Vite + React 19 + TypeScript, Tailwind CSS v4, shadcn/ui.
See `AGENTS.md` for how the code is organized and `docs/API.md` for the backend API.

## Prerequisites

- Node.js (tested with 26)
- pnpm 12.6.0, the version pinned in `package.json`'s `packageManager` field

If `pnpm` prints `Failed to switch pnpm to v12.6.0 ... ENOEXEC`, your global
pnpm is too old (10.x) to install pnpm 12 correctly. Upgrade it with
`brew upgrade pnpm` (or `npm i -g pnpm@12.6.0`), or run commands through
`npx pnpm@12.6.0 <command>`.

## Run it

```sh
pnpm install
pnpm dev
```

Open http://localhost:5173. By default `pnpm dev` serves a mock API (MSW)
in the browser, so no backend is needed.

To use the Go server instead, copy `.env.example` to `.env.local` and set
`VITE_API_MOCKS=off`. Vite then proxies `/api` to `VITE_API_TARGET`
(default `http://localhost:8080`). A one-off run works too:
`VITE_API_MOCKS=off pnpm dev`.

## Other commands

| Command          | What it does                                             |
| ---------------- | -------------------------------------------------------- |
| `pnpm build`     | Type-check, then build to `dist/`                        |
| `pnpm preview`   | Serve the `dist/` build                                  |
| `pnpm test`      | Run the Vitest suite once (`pnpm test:watch` to watch)   |
| `pnpm typecheck` | Type-check only                                          |
| `pnpm lint`      | Run Oxlint                                               |
| `pnpm iconfont`  | Rebuild the icon font from `src/assets/icons/*.svg`      |
