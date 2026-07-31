# Spec 021 — Images in task bodies

**Status:** sketch · **Umbrella:** `000-umbrella-architecture.md` ·
**Depends on:** 004 (execution backbone — `tasks.body`), 008 (worklode plugin — `lode task brief`),
020 (inbox import — supplies untrusted body text)

## Purpose & scope

`tasks.body` is markdown, and markdown already has image syntax. What is missing is somewhere
for the bytes to live and a path that gets them there without the author thinking about it.
This spec adds a content-addressed blob store, one upload endpoint, one download route, and the
CLI ergonomics that make `![alt](./shot.png)` in a body file Just Work.

The motivating user is a designer whose bug reports are "the map flashes narrow for one frame
when you scroll back up at 390px". That report is a 2-second screen capture and two screenshots.
Prose is a lossy re-encoding of it.

**In scope:** blob storage and dedup, upload/download surfaces, local-reference rewriting on
task create/update, rendering in the web UI and CLI, `brief` integration, the sanitisation
that rendering markdown as HTML now requires.

**Out of scope:** video (see *Size and media types*), attachments on design documents
(spec 014 — same blob store, different reference table, later), mirroring remote images found
in imported GitHub issue bodies (see *Open questions*), blob garbage collection (see
*Lifecycle*).

---

## 1. Storage

Content-addressed `bytea` in Postgres, mirroring `skill_versions.archive` (spec 016,
migration `0007`). That precedent already carries binary payloads through this schema, this
store layer, and this HTTP stack, so there is no new operational surface — no bucket, no
credentials, no lifecycle policy, no second thing to back up.

```sql
-- 0009_blobs.up.sql

CREATE TABLE blobs (
    hash       text PRIMARY KEY,              -- sha256, lowercase hex, 64 chars
    media_type text NOT NULL,                 -- server-sniffed, never client-supplied
    size       bigint NOT NULL,
    content    bytea NOT NULL,
    created_at timestamptz NOT NULL
);

-- Provenance and a GC root. Not the render path: rendering reads the body's
-- markdown, not this table.
CREATE TABLE task_blobs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hash       text NOT NULL REFERENCES blobs(hash) ON DELETE RESTRICT,
    alt        text NOT NULL DEFAULT '',
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, hash)
);
```

Dedup is free: the same screenshot attached to five tasks is one row. `blobs` is deliberately
not task-scoped — spec 014 design-document sections will want the same bytes behind a
`section_blobs` table without a migration or a copy.

`ON DELETE RESTRICT` on `task_blobs.hash` is the safety interlock: no code path can delete a
blob out from under a body that still references it.

---

## 2. Reference syntax

Bodies store a **root-relative URL**:

```markdown
![map flashes narrow on scroll-up](/blob/9f2a…c1)
```

No custom scheme, no macro, no rewriting on the render path. The web UI is served from the
same origin, so the browser resolves it with zero help. A body pasted into any other markdown
tool degrades to a broken image rather than to visible sigil noise.

The two consumers that are *not* same-origin resolve the prefix themselves, and both already
know the server base URL:

- **CLI** — `cli.Client` holds `server`; rewrite `/blob/` → `<server>/blob/` before rendering.
- **Agents** — never parse the body. `lode task brief --json` gains a resolved `attachments`
  array (§6).

`/blob/{hash}` sits outside `/api/v1` on purpose: it is not a JSON API, it is a static asset
route, and it must be reachable by both auth schemes (§4).

---

## 3. Surfaces

| Surface | Purpose |
|---|---|
| `POST /api/v1/blobs` | Raw body upload. `Content-Type` is advisory; the server sniffs. Returns `{hash, media_type, size, url}`. Idempotent — re-uploading identical bytes returns `200` with the existing row, never a duplicate. |
| `GET /blob/{hash}` | Serve the bytes. Immutable caching, hardened headers (§5). |
| `lode task attach <id> <file>…` | Upload each file, write `task_blobs`, append `![<basename>](/blob/…)` to the body. |
| `lode task attach <id> -` | Read one blob from stdin. Pairs with `pngpaste - \| lode task attach WL-42 -`. |
| `lode task add --body-file`, `lode task edit --body-file` | **Reference rewriting** (§7) — the main event. |

`lode task attach` is the explicit path. Reference rewriting is the one people will actually
use, because it requires knowing nothing.

---

## 4. Auth

`/blob/{hash}` must serve both a browser `<img>` (session cookie, `s.webAuth`) and a CLI or
agent fetch (bearer token, `s.auth`). Add one middleware:

```go
// eitherAuth accepts a bearer token or a web session. Blobs are the only
// route both audiences fetch directly.
func (s *server) eitherAuth(next http.HandlerFunc) http.Handler
```

A content-addressed URL is unguessable, and that is **not** the access control. Worklode task
bodies carry pre-release design work; an unauthenticated blob route is a public bucket with
extra steps. Authenticate, and let the hash do dedup only.

---

## 5. Serving hardening

A blob is bytes an authenticated user uploaded, served from the app's own origin. Untrusted
content on a trusted origin is the whole XSS problem, so the response headers do the work:

```
Content-Type: <sniffed media type>
X-Content-Type-Options: nosniff
Content-Security-Policy: default-src 'none'; sandbox
Cache-Control: private, max-age=31536000, immutable
```

- **Sniff server-side** with `http.DetectContentType` and store the result. A client that
  labels a `.html` payload `image/png` gets the sniffed type, so it is served as HTML — which
  is why the CSP and the allowlist both exist rather than either alone.
- **Allowlist:** `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/svg+xml`.
  Anything else is `415`.
- **SVG is accepted deliberately.** It is a first-class asset in the repos this serves (the
  CMS ships hand-optimised SVG), and rejecting it would push authors to lossy PNG screenshots
  of vector work. `default-src 'none'; sandbox` neuters script inside it. Note that `sandbox`
  on an `<img>`-loaded SVG is belt-and-braces — SVG loaded via `<img>` cannot run script in
  any current browser regardless; the CSP covers the case where someone navigates to the blob
  URL directly.

`Cache-Control` is `private`, not `public` as `skillArchive` uses: these responses are
per-user-authenticated and must not land in a shared proxy cache.

### Size and media types

`maxAPIBody` (1 MiB) is applied by `readJSON` and does not bind a raw-body route. The upload
handler sets its own limit:

```go
const maxBlobBytes = 10 << 20 // 10 MiB
```

10 MiB comfortably fits retina screenshots and short GIFs. It does not fit screen recordings,
which is the honest limit of the Postgres-bytea approach — a 40 MiB MP4 per bug report is
where object storage stops being premature. **v1 is images only.** When video arrives, `blobs`
grows a nullable `external_url` and the bytes move; the reference syntax and every consumer
stay unchanged.

---

## 6. Rendering

### Web UI

`templates/task.html` renders `<pre>{{.Task.Body}}</pre>` today. Images require real markdown
rendering, which turns the body into HTML — and **task bodies are untrusted**. Spec 020's
`lode inbox import` writes GitHub issue text straight into `tasks.body`, so anyone who can
open an issue on a mapped repo can put markup in a task body. Rendering that unsanitised is
stored XSS with an ingestion pipeline attached.

Therefore:

- Render with **goldmark** (already an indirect dependency via glamour; promote it to direct).
- Disable raw HTML: `goldmark.WithParserOptions()` without `html.WithUnsafe()`. Goldmark
  escapes raw HTML by default, so this is a matter of not opting out.
- Restrict image `src` to `/blob/<64 hex>` at render time. An imported issue body containing
  `![](https://tracker.example/pixel.png)` must not turn every task page view into a callback
  to a third party.
- Restrict link `href` schemes to `http`, `https`, `mailto`. Goldmark's default link handling
  permits `javascript:`; the CMS hit precisely this class of bug in `fix(security): reject
  non-http(s) licence URLs before rendering as href`.

The board and project pages keep showing titles only; nothing changes there.

### CLI

`cli.Markdown` already routes through glamour. Two changes:

1. Rewrite `/blob/…` → `<server>/blob/…` so the URL is complete and terminal-clickable.
2. Glamour renders `![alt](url)` as alt text plus the link, which is the correct v1 behaviour.

Inline terminal images (iTerm2 / Kitty / WezTerm escape sequences) are a deliberate v2 — the
capability detection and the graceful fallback are more code than the feature is worth before
anyone has asked.

---

## 7. Reference rewriting

The feature that makes the rest of it invisible. An author writes an ordinary markdown file
next to their screenshots:

```markdown
The map flashes to its narrow inset for one frame on scroll-up at 390px:

![before](./shots/flash-390.png)
![expected](./shots/expected-390.png)
```

```bash
lode task add --project cms --title "Map flashes narrow on scroll-up" --body-file ./bug.md
```

`lode` walks the parsed markdown for image destinations that are **local relative paths**
(no scheme, no leading `/`), resolves each against the body file's directory, uploads it,
and rewrites the destination to `/blob/<hash>` before the body is sent. Same pass on
`lode task edit --body-file`.

Rules that keep it predictable:

- Only image nodes. Links to local files are left alone and reported as a warning.
- Absolute paths and any destination with a scheme are left untouched.
- Path traversal above the body file's directory is an error, not a silent skip.
- A missing file is an error — the whole command fails before creating the task, so an author
  never ends up with a task whose body points at images that were never uploaded.
- `--body` (inline string) does **not** rewrite. There is no base directory to resolve
  against, and inline bodies come from scripts.
- `--no-upload` opts out.

Uploads happen before the create/update call, so the task is written once, with final content.

---

## 8. Brief integration

Agents must not parse markdown to find pictures. `lode task brief --json` gains:

```json
"attachments": [
  {
    "alt": "map flashes narrow on scroll-up",
    "url": "https://worklode.dev.sunstoneinstitute.ai/blob/9f2a…c1",
    "media_type": "image/png",
    "size": 214003
  }
]
```

Derived from `task_blobs` joined to `blobs`, with `alt` taken from the row. URLs are absolute
and fetchable with the agent's existing bearer token, so a vision-capable agent can read the
screenshot the reporter actually saw. This keeps the brief's "no file spelunking" contract
(spec 008) intact for a class of context it previously could not carry at all.

---

## 9. Lifecycle

`task_blobs` rows are written by `lode task attach` and by reference rewriting. They are the
GC root — and **v1 does not GC**.

The reason to defer it: a body is free text, so a reference can be edited away, leaving a
`task_blobs` row that no rendered body cites. Reconciling that means re-parsing bodies on a
schedule to prune stale rows before any blob becomes collectable. That is a real piece of
machinery to protect against leaking a few megabytes of screenshots, on an install with two
projects.

Deleting a task cascades its `task_blobs` rows; `ON DELETE RESTRICT` on `blobs.hash` keeps the
bytes. A `lode admin blob gc --dry-run` verb is the natural v2 once there is enough data to
know whether it matters.

---

## Open questions

- **Q021.1 — Mirror remote images on import?** Imported GitHub issue bodies reference
  `https://user-images.githubusercontent.com/…`. Those URLs require GitHub auth for private
  repos and are not stable forever, so an imported bug report's screenshots render as broken
  images. Mirroring them into `blobs` at import time fixes it and makes the import a
  content-fetching operation with an SSRF surface. Deferred; §6's `src` restriction means they
  render as broken rather than as live third-party requests in the meantime.
- **Q021.2 — Alt text as the accessibility contract.** `lode task attach` defaults `alt` to
  the file basename, which is not alt text. Worth a `--alt` flag; worth more as a lint that
  refuses `![](…)` on a task whose concern is `usability`.
- **Q021.3 — Does `lode task attach` belong at all**, given §7 covers the authoring case? It
  covers the *append to an existing task* case (`pngpaste - | lode task attach WL-42 -`),
  which rewriting does not. Keep both, but if one is cut, cut `attach`.

---

## Acceptance criteria

1. `POST /api/v1/blobs` with a PNG returns a hash; re-posting identical bytes returns the same
   hash and creates no second row.
2. A payload whose sniffed type is outside the allowlist gets `415`; a payload over 10 MiB
   gets `413`.
3. `GET /blob/{hash}` serves the bytes to both a bearer token and a web session, and `401`s
   with neither.
4. `lode task add --body-file` on a markdown file referencing two local PNGs creates one task
   whose body cites `/blob/…` twice, with two `blobs` rows and two `task_blobs` rows.
5. A body containing `<script>alert(1)</script>`, `[x](javascript:alert(1))`, and
   `![](https://evil.example/p.png)` renders in the web UI with the script escaped, the link
   inert, and the remote image dropped.
6. `lode task show` prints absolute, clickable blob URLs.
7. `lode task brief --json` returns an `attachments` array with absolute URLs and media types.
8. Deleting a task removes its `task_blobs` rows and leaves `blobs` intact.
