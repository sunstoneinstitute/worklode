---
status: draft
issued: 2026-07-31
requires:
- docs/specs/004-execution-backbone.md
- docs/specs/008-worklode-plugin.md
- docs/specs/020-inbox-import.md
---
# Spec 021 — Images and attachments on tasks

## 0. Purpose & scope {#sec-0}

`tasks.body` is markdown, and markdown already has image syntax. What is missing is somewhere
for the bytes to live and a path that gets them there without the author thinking about it.
This spec adds a content-addressed blob store on Hetzner Object Storage, the database index
that makes those blobs referenceable and collectable, and the CLI ergonomics that make
`![alt](./shot.png)` in a body file Just Work.

The motivating user is a designer whose bug reports are "the map flashes narrow for one frame
when you scroll back up at 390px". That report is a screen capture and two screenshots. Prose
is a lossy re-encoding of it.

Blobs serve two distinct jobs, and the difference runs through the whole spec:

- **Embedded** — an image or video the body cites inline. Rendered in place.
- **Attached** — a log, a core dump, a HAR file, a dataset. Downloadable, never rendered.

Every attachment is a blob; only image and video blobs are embeddable.

**In scope:** object storage and the database index, upload/download surfaces, local-reference
rewriting on task create/update, mirroring remote images on import, rendering in the web UI and
CLI, `brief` integration, two-directional garbage collection, and the sanitisation that
rendering markdown as HTML now requires.

**Out of scope:** attachments on design documents (spec 025 — same blob store, a second
reference table, later); the alt-text lint (§14, v2); inline terminal rendering (§8, v2).

---

## 1. Storage {#sec-1}

Bytes live in **Hetzner Object Storage** (S3-compatible, path-style addressing, the same
service the fleet already uses for CNPG and Velero backups). Postgres holds the index and the
reference graph, never the payload.

The object key is derived from the content hash, sharded two hex characters deep so the
orphan sweep (§11) can parallelise over 256 prefixes:

```
blobs/<hash[0:2]>/<hash>          e.g. blobs/9f/9f2a…c1
```

```sql
-- 0009_blobs.up.sql

CREATE TABLE blobs (
    hash       text PRIMARY KEY,              -- sha256, lowercase hex, 64 chars
    media_type text NOT NULL,                 -- server-sniffed, never client-supplied
    size       bigint NOT NULL,
    created_at timestamptz NOT NULL
);

-- The reference graph. A blobs row with no referencing row here is garbage (§11).
CREATE TABLE task_blobs (
    task_id    text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    hash       text NOT NULL REFERENCES blobs(hash) ON DELETE RESTRICT,
    filename   text NOT NULL,                 -- for Content-Disposition and display
    embedded   boolean NOT NULL DEFAULT false,  -- derived: the body cites /blob/<hash>
    attached   boolean NOT NULL DEFAULT false,  -- explicit: lode task attach
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, hash),
    CONSTRAINT task_blobs_referenced CHECK (embedded OR attached)
);

CREATE INDEX task_blobs_hash_idx ON task_blobs (hash);
```

No object key column: the key is a pure function of the hash, so storing it would create a
second source of truth that can disagree.

Dedup is free — the same screenshot on five tasks is one object and one `blobs` row.
`blobs` is deliberately not task-scoped, so spec 025 document sections can reference the same
bytes through a `section_blobs` table without a migration or a copy.

`embedded` is **derived**: on every task create and update, the body is parsed, its
`/blob/<hash>` references are extracted, and `embedded` is reconciled to match in the same
transaction. Edit an image out of the body and the flag clears on the next write, which is
what makes GC honest — a reference the body no longer makes must not keep bytes alive.

`attached` is **declared**: set by `lode task attach`, cleared by `lode task detach`. It
survives body edits because it was never in the body.

A row needs at least one of the two; the `CHECK` enforces it, and reconciliation deletes any
row that would fail it.

`ON DELETE RESTRICT` on `task_blobs.hash` is the interlock that makes GC safe: the database
refuses to delete a `blobs` row that anything still references, so a GC bug degrades into an
error rather than into a broken image.

---

## 2. Reference syntax and serving {#sec-2}

Bodies store a **root-relative, permanent URL**:

```markdown
![map flashes narrow on scroll-up](/blob/9f2a…c1)
```

`GET /blob/{hash}` authenticates the caller (§4), then **302-redirects to a short-lived
presigned object-storage URL**. This is the GitHub and GitLab pattern: the durable identifier
lives in the content, the credential lives in a URL that expires in minutes, and the bytes
never transit the application. That last property is what makes a 100 MiB screen recording
affordable to serve.

Presign parameters carry the headers, since the browser sees the object store's response and
not ours:

| Header | Source |
|---|---|
| `Content-Type` | Set as object metadata at upload, and overridden per-request via `response-content-type` from `blobs.media_type` |
| `Content-Length` | The object store's own, always correct |
| `Content-Disposition` | `response-content-disposition` — `inline` for embeddable types, `attachment; filename="…"` for everything else |
| `Cache-Control` | `response-cache-control: private, max-age=31536000, immutable` — safe because the URL is content-addressed |

Setting `Content-Type` at PUT time *and* overriding on presign is deliberate belt-and-braces:
the override is what actually reaches the browser, and the stored metadata keeps the object
self-describing for anyone reading the bucket directly.

The redirect itself is `Cache-Control: private, max-age=60`, comfortably inside the presign
TTL of 5 minutes, so a page with twenty images issues twenty redirects once and then serves
from cache.

`/blob/{hash}` sits outside `/api/v1` on purpose: it is an asset route rather than a JSON API,
and it must be reachable by both auth schemes.

**Verify before building:** Hetzner Object Storage is Ceph RADOS Gateway behind an S3 API, and
presigned GET with `response-*` overrides is standard SigV4 that it should honour. Confirm
against the live bucket in the first task. If an override turns out to be unsupported, the
fallback is to stream the object through the server with our own headers — simpler, same
external behaviour, and it costs the app egress and a held connection per download.

---

## 3. Surfaces {#sec-3}

| Surface | Purpose |
|---|---|
| `POST /api/v1/blobs` | Raw body upload, streamed (§5). Returns `{hash, media_type, size, url}`. Idempotent — identical bytes return `200` and the existing row, never a duplicate object. |
| `GET /blob/{hash}` | Authenticate, then redirect to a presigned URL (§2). |
| `GET /api/v1/tasks/{id}/blobs` | List a task's blobs: hash, filename, media type, size, embedded/attached. |
| `POST /api/v1/tasks/{id}/blobs` | Attach an already-uploaded hash to a task (`attached = true`). |
| `DELETE /api/v1/tasks/{id}/blobs/{hash}` | Clear `attached`; the row goes if `embedded` is also false. |
| `lode task attach <id> <file>…` | Upload, then attach. Images additionally get `![<basename>](/blob/…)` appended to the body. |
| `lode task attach <id> -` | Read one blob from stdin. Pairs with `pngpaste - \| lode task attach WL-42 -`. |
| `lode task detach <id> <hash>` | The inverse. Warns if the body still embeds it. |
| `lode task add --body-file`, `lode task edit --body-file` | **Reference rewriting** (§7) — the main event. |
| `lode admin blob gc [--dry-run]` | Both GC sweeps (§11). |

`lode task attach` is the explicit path and the only path for non-embeddable files. Reference
rewriting is what people will actually use for images, because it requires knowing nothing.

---

## 4. Auth {#sec-4}

`/blob/{hash}` must serve both a browser `<img>` (session cookie, `s.webAuth`) and a CLI or
agent fetch (bearer token, `s.auth`). One middleware covers it:

```go
// eitherAuth accepts a bearer token or a web session, and mirrors webAuth's
// pass-through when no web auth provider is configured.
func (s *server) eitherAuth(next http.HandlerFunc) http.Handler
```

**It must mirror `webAuth`'s bypass exactly.** `webAuth` (`internal/api/oidcweb.go:57`) passes
every request through when neither OIDC nor GitHub login is configured — the read-only web UI
is unauthenticated on such an install. A blob route that authenticated unconditionally would
render the task page fine and 401 every `<img>` on it. Consistency with the surrounding UI wins
here: the blob route is not the place to unilaterally tighten the installation's auth model.

The consequence follows the UI's auth model rather than restating it: an install with no web
auth provider serves no web surface at all unless it sets `LODE_WEB_OPEN`, and the blob route
inherits exactly that — refused on a closed instance, open on one that opted in. The blob route
is still not the place to tighten the auth model unilaterally; it is now inheriting a model that
is closed by default.

Where web auth *is* configured, a content-addressed URL is unguessable and that is **not** the
access control. Task bodies carry pre-release design work; the hash does dedup, the middleware
does access.

The bucket itself stays private in every case — presigned URLs are the only anonymous read
path, and they expire.

Session cookies are already `SameSite=Lax` (`internal/api/cliauth.go:130`, `oidcweb.go:97`,
`githubweb.go:100`). That is load-bearing here: Lax withholds the cookie from cross-site
subresource loads, so an attacker page embedding `<img src="https://worklode/blob/…">` gets a
401 rather than a probe for which blobs a logged-in victim can see. Keep it.

---

## 5. Upload {#sec-5}

**Stream; never buffer the payload in memory.** Content addressing means the hash is not known
until the last byte, so the handler cannot decide where bytes belong until it has seen them
all:

1. `http.MaxBytesReader(w, r.Body, maxBlobBytes)`.
2. `os.CreateTemp` in the server's spool directory; `defer` both close and remove so every
   exit path cleans up.
3. Copy request body → temp file through an `io.TeeReader` into a `sha256` hasher, capturing
   the first 512 bytes for sniffing.
4. Look up the hash. **Present → discard the temp file and return the existing row.** A
   re-uploaded screenshot costs one query and zero object-store traffic.
5. Absent → `PutObject` streaming from the rewound temp file, then insert the `blobs` row.

`readJSON`'s 1 MiB `maxAPIBody` does not apply — this route takes a raw body and sets its own
limit:

```go
const maxBlobBytes = 100 << 20 // 100 MiB
```

100 MiB fits screen recordings, which is the point: the bug reports this spec exists to carry
are frequently motion, and a still frame of a one-frame flash proves nothing.

**Write ordering is object-then-row, always.** A failure after the PUT leaves an orphan object,
which §11 sweeps. The reverse order would leave a `blobs` row pointing at nothing, which
renders as a permanently broken image. Both failure modes are possible; only one is recoverable
without a human, so the design fails toward that one.

The server sniffs with `http.DetectContentType` over the first 512 bytes and stores the result.
A client's `Content-Type` header is advisory and never persisted — a payload labelled
`image/png` that sniffs as HTML is stored, and served, as HTML, which is why §6's headers exist
rather than relying on the label.

| Class | Types | Treatment |
|---|---|---|
| **Embeddable image** | `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/svg+xml` | Rendered inline; `Content-Disposition: inline` |
| **Embeddable video** | `video/mp4`, `video/webm` | Rendered inline via `<video>`; `inline` |
| **Attachment** | anything else | Never rendered; `Content-Disposition: attachment` |

Nothing is rejected on type. A core dump is a legitimate attachment, and an allowlist that
blocks it buys nothing once §6 guarantees non-embeddable types are never served inline.

**SVG is embeddable deliberately.** It is a first-class asset in the repos this serves, and
rejecting it would push authors into lossy PNG screenshots of vector work. §6's CSP neuters
script inside it.

---

## 6. Serving hardening {#sec-6}

A blob is bytes an authenticated user uploaded. The redirect target is a different origin from
the app, which is itself a useful boundary — a hostile SVG or HTML payload executes in the
object store's origin, where there is no session cookie and nothing to steal.

The redirect response carries:

```
Cache-Control: private, max-age=60
Referrer-Policy: no-referrer
```

and the presigned URL carries, via `response-*` overrides:

```
Content-Type: <sniffed media type>
Content-Disposition: inline | attachment; filename="…"
X-Content-Type-Options: nosniff        (bucket-level default; see below)
Content-Security-Policy: default-src 'none'; sandbox
```

Hetzner's S3 API does not expose `response-content-security-policy`; `Content-Security-Policy`
and `X-Content-Type-Options` are therefore set as **object metadata at upload time**, where
RGW returns them on every GET. Confirm this in the same first task that verifies presign
overrides — if the gateway strips unknown metadata headers, `Content-Disposition: attachment`
on every non-embeddable type is the fallback that carries the security weight on its own.

The task page's own CSP must list the object-storage endpoint in `img-src` and `media-src`,
since that is where the redirect lands.

---

## 7. Reference rewriting {#sec-7}

The feature that makes the rest of it invisible. An author writes ordinary markdown next to
their screenshots:

```markdown
The map flashes to its narrow inset for one frame on scroll-up at 390px:

![before](./shots/flash-390.png)
![expected](./shots/expected-390.png)
```

```bash
lode task add --project cms --title "Map flashes narrow on scroll-up" --body-file ./bug.md
```

`lode` walks the parsed markdown for image destinations that are **local relative paths** (no
scheme, no leading `/`), resolves each against the body file's directory, uploads it, and
rewrites the destination to `/blob/<hash>` before the body is sent. Same pass on
`lode task edit --body-file`.

Rules that keep it predictable:

- Only image nodes. A link to a local file is left alone and reported as a warning — use
  `lode task attach` for those.
- Absolute paths and any destination carrying a scheme are untouched.
- Path traversal above the body file's directory is an error.
- A missing file is an error, and the whole command fails before the task is written, so an
  author never ends up with a body pointing at images that were never uploaded.
- `--body` (inline string) does not rewrite: there is no base directory to resolve against,
  and inline bodies come from scripts.
- `--no-upload` opts out.

Uploads complete before the create or update call, so the task is written once, with final
content, and `embedded` reconciliation (§1) sees the rewritten body.

---

## 8. Rendering {#sec-8}

### 8.1 Web UI {#sec-8.1}

`templates/task.html` renders `<pre>{{.Task.Body}}</pre>` today. Images require real markdown
rendering, which turns the body into HTML — and **task bodies are untrusted**. Spec 020's
`lode inbox import` writes GitHub issue text straight into `tasks.body`, so anyone who can open
an issue on a mapped repo can put markup in a task body. Rendering that unsanitised is stored
XSS with an ingestion pipeline attached.

The pipeline mirrors what GitHub does — render permissively, then sanitise the **output HTML**:

1. **goldmark** with the GFM extension set, `html.WithUnsafe()` **enabled** so raw HTML passes
   through to the sanitiser rather than being escaped at the parser. (goldmark is already an
   indirect dependency via glamour; promote it to direct.)
2. **bluemonday** `UGCPolicy()` — GitHub's allowlist in all but name: headings, lists, tables,
   `code`/`pre`, `blockquote`, inline emphasis, `a`, `img`, and GFM task-list checkboxes, with
   everything else stripped. New direct dependency.
3. Policy tightenings on top of `UGCPolicy`:
   - `img[src]` and `video[src]`/`source[src]` matched against `^/blob/[0-9a-f]{64}$`. An
     imported issue body containing `![](https://tracker.example/pixel.png)` must not turn
     every page view into a callback to a third party. §12 mirrors those into blobs on import
     precisely so this restriction costs nothing.
   - `a[href]` limited to `http`, `https`, `mailto`. `UGCPolicy` covers this and adds
     `rel="nofollow"`; assert it in a test regardless — the CMS shipped this exact bug in
     `fix(security): reject non-http(s) licence URLs before rendering as href`.
   - `video` allowed with `controls`, `preload`, `poster`.

**CSRF.** Every web route is `GET` today (`server.go:323–330`) and the UI has no form that
mutates state, so there is nothing to forge. That is a property to keep rather than one to
assume: the moment a mutating web surface is added, it needs a per-session token and a
`SameSite=Strict` cookie for that route. Until then, the standing rule is that the web UI
performs no state change, and a test asserts the template set contains no `<form method="post">`.

The board and project pages keep showing titles only.

The UI is, in the reviewer's words, horrible. Making it less so is not this spec's job, and
rendered markdown with inline screenshots will move it in the right direction on its own.

### 8.2 CLI {#sec-8.2}

`cli.Markdown` already routes through glamour. Two changes:

1. Rewrite `/blob/…` → `<server>/blob/…` so URLs are complete and terminal-clickable.
2. `lode task show` gains an attachments list under the body — filename, size, media type, URL
   — since attached blobs appear nowhere in the markdown.

Glamour renders `![alt](url)` as alt text plus a link, which is correct for v1.

**v2 — authenticated browser hand-off.** The interesting terminal integration is not escape
sequences, it is `lode task view <id>` opening a browser tab that is already authenticated,
which makes screenshots work in cmux and any terminal with an embedded browser, with no
capability detection and no per-terminal image protocol. The mechanism already exists: the CLI
auth flow (`/auth/cli/login`, `internal/api/cliauth.go`) mints a session, so the same
short-lived-token-in-URL exchange can open a tab that lands logged in. Deferred, but it is the
shape to build, and inline escape-sequence images are not.

---

## 9. Attachments {#sec-9}

Non-embeddable files are why `lode task attach` earns its place. A crash report is a core dump
and a log; a rendering bug is a HAR file; a data bug is the CSV that triggers it. All are
blobs, none belong in the body.

```bash
lode task attach WL-42 ./crash.log ./heap.prof
lode task attach WL-42 ./flash-390.png        # image → also appended to the body
```

The distinction is entirely media-type driven: image and video get a markdown reference
appended and `embedded = true` on the reconciliation that follows; everything else gets
`attached = true` and appears only in the attachments list. `--no-embed` suppresses the append
for an image the author wants attached but not shown inline.

---

## 10. Brief integration {#sec-10}

Agents must not parse markdown to find pictures. `lode task brief --json` gains:

```json
"blobs": [
  {
    "url": "https://worklode.dev.sunstoneinstitute.ai/blob/9f2a…c1",
    "filename": "flash-390.png",
    "media_type": "image/png",
    "size": 214003,
    "embedded": true
  },
  {
    "url": "https://worklode.dev.sunstoneinstitute.ai/blob/3b71…9d",
    "filename": "crash.log",
    "media_type": "text/plain; charset=utf-8",
    "size": 88210,
    "embedded": false
  }
]
```

URLs are absolute and fetchable with the agent's existing bearer token, so a vision-capable
agent can read the screenshot the reporter actually saw, and any agent can pull the log. This
keeps the brief's "no file spelunking" contract (spec 008) intact for a class of context it
previously could not carry at all.

Alt text stays in the body markdown, where it belongs, and is not duplicated here.

---

## 11. Lifecycle and garbage collection {#sec-11}

Two sweeps, both in `lode admin blob gc`, both with a **24-hour grace period** so neither can
race an upload in flight.

### 11.1 Unreferenced blobs {#sec-11.1}

A `blobs` row with no `task_blobs` row sharing its hash is garbage:

```sql
SELECT b.hash FROM blobs b
 WHERE NOT EXISTS (SELECT 1 FROM task_blobs tb WHERE tb.hash = b.hash)
   AND b.created_at < now() - interval '24 hours';
```

Delete the **row first**, inside a transaction that re-checks the zero-reference condition,
then delete the object. Failure after the row delete leaves an orphan object, which the second
sweep catches. The reverse order would leave a row pointing at a deleted object — a permanently
broken image. Symmetric with the write path (§5): both fail toward orphan objects, never toward
dangling rows.

When spec 025 adds `section_blobs`, the `NOT EXISTS` grows a second clause. That is the one
place adding a reference table touches GC, and it is worth a comment in the migration saying so.

### 11.2 Orphan objects {#sec-11.2}

The other direction, which should find nothing and will occasionally find something, because
the write path deliberately creates orphans on partial failure. List the bucket under
`blobs/`, parallel over the 256 shard prefixes, and delete any key whose hash has no `blobs`
row and whose `LastModified` is older than the grace period.

`--dry-run` reports both sets without deleting, and is the default in the first release —
running the real sweep should be a deliberate act until the reference reconciliation in §1 has
been observed to behave.

Deleting a task cascades its `task_blobs` rows, which drops the reference count and makes its
blobs collectable on the next sweep unless another task shares them.

---

## 12. Mirroring on import {#sec-12}

Imported GitHub issue bodies reference `https://user-images.githubusercontent.com/…`. Those
URLs need GitHub auth for private repos and do not last forever, so an imported bug report's
screenshots would render as broken images — and §8 blocks remote `img src` outright, so they
would render as nothing at all.

`lode inbox import` therefore **fetches every remote image reference in a body and rewrites it
to `/blob/<hash>`**, using the same upload path as §5 and the installation's GitHub App token
for `githubusercontent.com`. Everything becomes a blob; nothing in a rendered body ever points
off-site.

This makes import a URL-fetching operation on attacker-influenced input, so it needs the usual
SSRF guard:

- Fetch only `https`, only from a host allowlist (`*.githubusercontent.com`,
  `github.com`) — the import path knows exactly which host it expects.
- Resolve and check the IP before connecting, rejecting private, loopback, link-local, and
  metadata ranges, with the check applied again on every redirect hop.
- Cap redirects at 3, the response at `maxBlobBytes`, and the whole fetch at a 30-second
  timeout.
- On any failure, leave the original URL in place and log it. A partially-mirrored import is
  better than a failed one, and §8 renders the leftover as nothing rather than as a beacon.

---

## 13. Configuration {#sec-13}

Server config (env, and the deployment's ConfigMap plus an ESO-provisioned secret):

| Key | Example |
|---|---|
| `LODE_BLOB_ENDPOINT` | `https://hel1.your-objectstorage.com` |
| `LODE_BLOB_BUCKET` | `sunstone-worklode-blobs` |
| `LODE_BLOB_REGION` | `hel1` |
| `LODE_BLOB_ACCESS_KEY` / `LODE_BLOB_SECRET_KEY` | 1Password → ESO, per the fleet's existing pattern |
| `LODE_BLOB_SPOOL_DIR` | temp-file directory for §5; defaults to `os.TempDir()` |

`aws-sdk-go-v2` with `UsePathStyle: true`, matching the `s3ForcePathStyle: true` the fleet
already sets for Velero and CNPG against this endpoint.

**Blobs are unconfigured by default.** With no endpoint set, uploads return `501` and every
other surface behaves exactly as it does today, so a local `docker compose` stack keeps working
with no bucket. Worth a `lode doctor` line rather than a silent absence.

---

## 14. Open questions {#sec-14}

- **Q021.1 — Alt text as an accessibility contract.** `lode task attach` defaults the appended
  alt text to the file basename, which is not alt text. A `--alt` flag is cheap; a lint that
  refuses `![](…)` on a task whose concern is `usability` is more valuable and more annoying.
  **v2.**
- **Q021.2 — Do embedded videos want a poster frame?** A `<video>` with no poster is a black
  rectangle until played, which is a poor answer to "show me the bug". Extracting frame 0
  server-side means an ffmpeg dependency in the server image. Deferred; authors can attach a
  still alongside.
- **Q021.3 — Bucket per environment or prefix per environment?** Dev and prod sharing a bucket
  under different key prefixes halves the credential management and makes a prod-to-dev data
  copy trivially wrong. Separate buckets is the safer default; confirm against how the fleet
  provisions the other buckets.
- **Q021.4 — The web UI is unauthenticated without an SSO provider.**
  *Resolved 2026-08-14.* The web surface now refuses to serve without a login provider unless
  `LODE_WEB_OPEN` is set (001 §6), and §4's mirroring means blobs inherit the closed default.
  Nothing in this spec changed.

---

## 15. Acceptance criteria {#sec-15}

1. `POST /api/v1/blobs` with a PNG returns a hash; re-posting identical bytes returns the same
   hash, creates no second row, and issues no second `PutObject`.
2. A 200 MiB upload gets `413`, and the server's memory does not track payload size on any
   upload — asserted by uploading 100 MiB with a bounded heap.
3. `GET /blob/{hash}` 302s to a presigned URL for both a bearer token and a web session. With a
   web auth provider configured it `401`s with neither; with no provider configured it refuses
   with a `503` unless `LODE_WEB_OPEN` is set, matching `webGuard`. The presigned response
   carries the sniffed `Content-Type`, a correct `Content-Length`, and `Content-Disposition:
   attachment` for a non-embeddable type.
4. `lode task add --body-file` on markdown referencing two local PNGs creates one task whose
   body cites `/blob/…` twice, with two `blobs` rows and two `task_blobs` rows at
   `embedded = true`.
5. Editing that body to drop one image clears its `embedded` flag and deletes the row; the next
   `lode admin blob gc` collects that blob and its object, and leaves the other.
6. `lode task attach` with a `.log` file creates a row at `attached = true, embedded = false`,
   appends nothing to the body, and the blob survives a body rewrite.
7. A body containing `<script>alert(1)</script>`, `[x](javascript:alert(1))`,
   `![](https://evil.example/p.png)`, and `<img src="/blob/../../etc/passwd">` renders with the
   script stripped, the link inert, and both images dropped — while `<b>`, tables, and task-list
   checkboxes survive.
8. An imported issue body with a `user-images.githubusercontent.com` image ends up citing
   `/blob/…`; one pointing at `http://169.254.169.254/…` is refused and left as-is.
9. `lode task brief --json` returns a `blobs` array with absolute URLs, media types, and the
   `embedded` flag.
10. An object with no `blobs` row and a `LastModified` older than the grace period is deleted by
    the orphan sweep; one newer than it is not.
11. Deleting a task cascades its `task_blobs` rows and leaves blobs that another task still
    references intact.
12. With no `LODE_BLOB_ENDPOINT`, uploads return `501` and every existing test still passes.
