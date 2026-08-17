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
corepack enable pnpm          # once per machine; pnpm version comes from package.json
pnpm install --frozen-lockfile && pnpm build
mkdir -p "$VAULT/.obsidian/plugins/worklode"
cp manifest.json main.js "$VAULT/.obsidian/plugins/worklode/"
```

`main.js` is not committed (it's build output), so a fresh clone must run the
build before copying. Then, in Obsidian: Settings → Community plugins (needs
Restricted Mode off) → enable "Worklode".

This package uses pnpm, pinned by `package.json`'s `packageManager` field. It is
the only Node package in the repo and shares no dependency tree with the Go
build.

## Settings

- **Base URL** — the worklode server, e.g. `https://worklode.example.com`.
- **Token** — a bearer token for an actor with read access, minted with
  `lode token create --actor <id>`.
- **Mount root** — the vault folder the mirror owns (default `Worklode`).
  Must be a single path segment: see Limits below. Saved a moment after you
  stop typing rather than on each keystroke, so an armed sync interval can't
  fire against a half-typed folder name. The first sync into a root that
  already holds notes the mirror did not write stops and asks — see
  Taking over a folder.
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
- **Mount root can't be nested.** It must be a single path segment: no `/` or
  `\`, no `..` anywhere in it, not `.` or `..` on its own, not blank, and no
  leading or trailing whitespace. `Team/Worklode` is refused, not partially
  honoured.
- **A sync deletes foreign notes under the mount root.** Every `.md` file
  under the root that the mirror does not currently produce is removed on each
  sync — including files the plugin never created. They are moved to the
  vault's trash (Settings → Files and links → Deleted files), so an accident
  is recoverable, but do not point the mount root at a folder of your own
  notes. The first sync into such a folder asks first: see below.
- **Purge deletes the whole mount folder.** The "Purge the Worklode folder"
  command removes everything under the mount root, including files the
  plugin did not create — not just the notes it manages. Unlike a sync, this
  is a permanent delete, not a move to trash.

## Taking over a folder

Before the first sync into a mount root, the plugin looks for `.md` files
under it that carry no `wl` frontmatter block — anything it did not write. If
it finds any, the sync stops and asks whether the folder is yours to take
over, naming what it found. Cancelling changes nothing; confirming records
the root and hands it to the mirror, which from then on deletes anything
under it the backbone does not imply.

The question is asked once per root name, and only when the root already has
foreign notes in it: a root the mirror created, or one that was empty, is
adopted silently. Confirmed roots are remembered in the plugin's
`data.json` (`adoptedRoots`), so pointing the setting back at a folder you
already confirmed does not ask again. Delete that entry to be asked afresh.
