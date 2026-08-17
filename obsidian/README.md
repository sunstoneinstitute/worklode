# Worklode for Obsidian

Mirrors a Worklode instance into an Obsidian vault: one folder per project,
one note per document and task, plus an index note. One-way by default:
everything under the mount folder is machine-owned, and any edit made inside
it is discarded on the next sync. Turning on **Write edits back** opens one
narrow return path — a task note's body — and nothing else; see Writing edits
back.

## Layout

Given a mount root of `Worklode` (the default):

```
Worklode/Worklode.md              # index: every synced project
Worklode/<project>/<project>.md   # project note: doc/task roll-up by state
Worklode/<project>/docs/<id>.md   # one note per synced document
Worklode/<project>/tasks/<id>.md  # one note per synced task
Worklode/_conflicts/<project>/    # only with write-back on; see below
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
- **Write edits back** — off by default. When on, an edit to a task note's
  body is pushed to Worklode on the next full sync. Nothing else a note shows
  is writable. See Writing edits back.
- **Sync interval (minutes)** — 0 (default) means manual only, via the
  "Worklode: Sync now" command. Every 5th automatic sync is a full one; the
  four in between fetch only the tasks that changed — see Limits.

## Limits

- **Read-only unless you turn write-back on**, and even then only a task
  note's body travels back. Everything under the mount root is rewritten
  unconditionally on every sync where its etag changed — an edit to anything
  else is not preserved, and does not error, it is just overwritten on the
  next sync. See Writing edits back.
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
- **Most automatic syncs are incremental, and an incremental sync is
  partial.** It asks the server for the tasks changed since the newest
  `updated_at` it has seen (`GET /api/v1/tasks?updated_since=`) and writes
  only task notes. Two consequences, both healed by the next full sync: a
  task deleted from the backbone never appears in a "what changed" answer, so
  its note stays until then — an incremental sync deletes nothing at all —
  and the project and index notes, whose roll-ups are built from a project's
  whole task set, are left exactly as the last full sync wrote them rather
  than re-rendered from a partial one. A full sync runs on plugin load, on
  "Worklode: Sync now", and on every 5th automatic tick; the watermark it
  keeps lives in `data.json` (`mirrorState`) and is discarded when the base
  URL, token or mount root changes, since it means nothing against a
  different backbone.
- **A server with no docs endpoint costs the doc notes only.** The plugin
  ships independently of the binary, so `GET /api/v1/docs` may be absent
  (404). Projects and tasks still mirror, the sync notice says doc notes were
  skipped, and existing doc notes are left in place rather than pruned — a
  missing route is no evidence the documents are gone. Any other API failure
  (401, 5xx, no connection) still fails the whole sync.
- **Purge deletes the whole mount folder.** The "Purge the Worklode folder"
  command removes everything under the mount root, including files the
  plugin did not create — not just the notes it manages. Unlike a sync, this
  is a permanent delete, not a move to trash.

## Writing edits back

Off unless **Write edits back** is on: it turns the mount root from
machine-owned into jointly written, which is a decision to make deliberately.

What travels back is one thing — **a task note's body**, the text below the
frontmatter. Everything else a note shows lives in the reserved `wl` block,
which is Worklode's; editing it changes nothing and is restored on the next
sync, the same way any etag mismatch is. State transitions are deliberately
not writable even though the API would accept a few of them: claiming and
finishing work belongs to `lode`, not to a text editor. Doc, project and index
notes are read-only outright.

The token needs write access to tasks (`PATCH /api/v1/tasks/{id}`), which an
ordinary non-admin token has.

**Full syncs only.** An incremental sync holds only the tasks that changed, so
it cannot tell an edited note from an untouched one. An edit therefore reaches
Worklode on the next *full* sync: immediately on "Worklode: Sync now" (always
full), on plugin load with Sync on startup, or on the 5th automatic tick — up
to five intervals away. Run "Sync now" when you want an edit to land now.

**Worklode wins a real conflict, and your text is never destroyed.** A note
whose body you changed while Worklode's copy of that task did not change is
pushed. If the task changed on both sides, the note is rewritten from Worklode
as usual and your body is saved beside it:

```
Worklode/_conflicts/<project>/<task id> <timestamp>.md
```

Conflict notes are yours to keep or delete: a sync never removes one, and they
are the only files under the mount root exempt from the delete pass. They
carry a `wl` block of their own, so the mirror recognises them as its own
rather than asking to take them over.

A push the server refuses (or a note that no longer parses) is reported in the
sync notice, with the detail in the console; the note is left exactly as you
edited it and the next full sync tries again.

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
