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

The index note is named after the mount root's own folder and lives inside
it, so a nested root of `Team/Worklode` puts it at
`Team/Worklode/Worklode.md` — the folder name, not the path.

Task notes carry `parent`/`children`/`blocks`/`blocked_by` as frontmatter
wikilinks, so Obsidian's graph view renders the task graph.

Every note carries a reserved `wl` frontmatter block — the backbone's fields,
the note's `etag`, and a record of what the plugin itself added, so a note can
be read back to exactly the body it was rendered from. A note opens with an
`# <title>` heading unless the body already brought its own H1 (a spec or a
plan normally does), which `wl.heading_added` records. `wl.serializer` is the
version of that rendering contract: a note written by an older plugin is
re-rendered on the next sync even when the backbone data has not changed,
because the `etag` covers the data and not the layout. Upgrading the plugin
therefore rewrites every note in the mount root once.

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

The plugin runs on desktop and mobile alike (`isDesktopOnly: false`): it
imports no Node builtin, hashing etags through the Web Crypto `crypto.subtle`
every Obsidian platform provides rather than `node:crypto`. Keep it that way —
a single `node:` import anywhere under `src/` puts mobile back out of reach.

This package uses pnpm, pinned by `package.json`'s `packageManager` field. It is
the only Node package in the repo and shares no dependency tree with the Go
build.

## Settings

- **Base URL** — the worklode server, e.g. `https://worklode.example.com`.
- **Token** — a bearer token for an actor with read access, minted with
  `lode token create --actor <id>`.
- **Mount root** — the vault folder the mirror owns (default `Worklode`).
  May be nested, e.g. `Team/Worklode`; every folder name in it has to be a
  plain name, see Limits below. Saved a moment after you
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
- **The mount root is checked folder name by folder name.** It may be nested
  — `Team/Worklode` is fine — but every name between the slashes must be
  non-blank, carry no leading or trailing whitespace, not be `.`, and contain
  no `..`. So is an empty name: a leading or trailing `/`, or `Team//Worklode`,
  is refused. A backslash is a forbidden character rather than a separator,
  so `Team\Worklode` is rejected outright instead of being read as two
  folders. A root that fails the check is refused whole, not partially
  honoured. Missing parent folders are created on the first write, and a
  purge removes the root itself while leaving its parents alone.
- **Project, doc, and task ids stay single-segment.** Each becomes one folder
  or file name under the root, so an id carrying `/`, `\`, `..`, or edge
  whitespace is skipped and reported as a conflict rather than nesting the
  note somewhere the mirror does not manage. Only the mount root — your own
  setting, surveyed before anything is deleted under it — may span folders.
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

The question is asked once per root path, and only when the root already has
foreign notes in it: a root the mirror created, or one that was empty, is
adopted silently. Confirmed roots are remembered in the plugin's
`data.json` (`adoptedRoots`), so pointing the setting back at a folder you
already confirmed does not ask again. Delete that entry to be asked afresh.
