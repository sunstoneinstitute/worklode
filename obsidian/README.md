# Worklode for Obsidian

Mirrors a Worklode instance into an Obsidian vault: one folder per project,
one note per document and task, plus an index note. It is a read-only,
one-way mirror — everything under the mount folder is machine-owned, and any
edit made inside it is discarded on the next sync.

## Layout

Given a mount root of `Worklode` (the default):

```
Worklode/Worklode.md              # index: every synced project
Worklode/<project>/<project>.md   # project note: doc/task roll-up by state
Worklode/<project>/docs/<id>.md   # one note per synced document
Worklode/<project>/tasks/<id>.md  # one note per synced task
```

Task notes carry `parent`/`children`/`blocks`/`blocked_by` as frontmatter
wikilinks, so Obsidian's graph view renders the task graph.

## Build and install

```bash
npm ci && npm run build
mkdir -p "$VAULT/.obsidian/plugins/worklode"
cp manifest.json main.js "$VAULT/.obsidian/plugins/worklode/"
```

`main.js` is not committed (it's build output), so a fresh clone must run the
build before copying. Then, in Obsidian: Settings → Community plugins (needs
Restricted Mode off) → enable "Worklode".

## Settings

- **Base URL** — the worklode server, e.g. `https://worklode.example.com`.
- **Token** — a bearer token for an actor with read access, minted with
  `lode token create --actor <id>`.
- **Mount root** — the vault folder the mirror owns (default `Worklode`).
  Must be a single path segment: see Limits below.
- **Projects** — comma-separated project ids to sync; empty syncs every
  project the token can read.
- **Sync on startup** — run a sync automatically when the plugin loads.
- **Sync interval (minutes)** — 0 (default) means manual only, via the
  "Worklode: Sync now" command.

## Limits

- **Read-only.** The plugin never writes back to the backbone. Everything
  under the mount root is rewritten unconditionally on every sync where its
  etag changed — a local edit there is not preserved, and does not error, it
  is just silently overwritten on the next sync.
- **Plaintext token.** The token is stored in the vault's
  `.obsidian/plugins/worklode/data.json`, unencrypted. Anyone with read
  access to the vault (including a sync service backing it up) can read it.
- **Desktop only.** `manifest.json` sets `isDesktopOnly: true`: etag
  computation uses Node's `node:crypto`, which is not available on Obsidian
  mobile.
- **Mount root can't be nested.** It must be a single path segment — no `/`,
  no `.`, no `..`. `Team/Worklode` is refused, not partially honoured.
- **Purge deletes the whole mount folder.** The "Purge the Worklode folder"
  command removes everything under the mount root, including files the
  plugin did not create — not just the notes it manages.
