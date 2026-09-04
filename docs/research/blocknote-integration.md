# BlockNote as Worklode's document editor

Research note, 2026-09-03. Question: can
[`TypeCellOS/BlockNote`](https://github.com/TypeCellOS/BlockNote) replace the
plain Markdown input in Worklode's cockpit without changing the document
format or weakening its document rules?

**Short answer: not yet as the primary editor. Run a narrow spike behind an
opt-in switch.** BlockNote is a capable, maintained editor and its core license
fits Worklode. It can mount in a server-rendered page without React. The hard
part is the data boundary: Worklode stores complete Markdown with YAML
frontmatter, stable `{#sec-N}` heading anchors, typed links and other declarations.
BlockNote says its Markdown import and export are lossy and cover only a small
CommonMark and GitHub Flavored Markdown subset. Using that conversion on save
could silently rewrite or discard authoritative document data.

The smallest useful test is therefore not a polished editor. It is one
client-side editor on a draft document, with the original textarea still
available, that refuses to save when a Markdown round trip changes anything
outside an explicitly accepted normalization set.

Method: primary sources only. BlockNote facts come from its official docs,
repository manifests, license, changelog and GitHub releases. Worklode facts
come from the current source tree. BlockNote was at v0.54.0, released
2026-08-13, when checked. No code was changed or run.

---

## Fit with the current cockpit

Worklode renders the cockpit on the server with Go and `templ`. Its shared
Markdown input is progressively enhanced: a plain `<textarea>` remains usable
without JavaScript, while `/assets/mdinput.js` adds preview, dictation and emoji
completion. Preview is produced and sanitized by the server. Documents are
currently read-only in the cockpit; writes go through the whole-body document
API and the CLI. The API stores the full Markdown body, including frontmatter,
and derives document fields, sections and relations from it
([`internal/ui/mdinput.templ`](../../internal/ui/mdinput.templ),
[`internal/ui/docs.templ`](../../internal/ui/docs.templ),
[`internal/model/doc.go`](../../internal/model/doc.go),
[`internal/api/docs.go`](../../internal/api/docs.go)).

The whole-body update API has no `ETag`, version precondition or equivalent
compare-and-swap field. Two browser sessions could therefore save in sequence
and the later save would silently replace the earlier one. A browser editor
needs a server-enforced conflict check before it can be more than a single-user
spike. This is separate from real-time collaboration.

BlockNote does not require a React application. `@blocknote/core` exposes
`BlockNoteEditor.create()` and `editor.mount(element)` for plain JavaScript.
The trade-off is that the ready-made toolbar, side menu and suggestion menus
are React components; a vanilla integration must build and connect those UI
elements itself. BlockNote recommends React for its full out-of-the-box
experience ([vanilla JavaScript guide](https://www.blocknotejs.org/docs/getting-started/vanilla-js)).

That leaves two integration shapes:

| Shape | Cost | Fit |
|---|---|---|
| Vanilla `@blocknote/core`, mounted into a `templ` page | Add a JavaScript build and write the required editor controls | Best spike. It preserves Worklode's server-rendered shell and does not bring React into every page. |
| A small React island using `@blocknote/react` and one UI package | Add React, React DOM and a UI package, but get the maintained menus and toolbars | Better only if the spike proves BlockNote and the team accepts a larger client bundle and a second UI system. BlockNote itself is client-only ([getting started](https://www.blocknotejs.org/docs/getting-started)). |

Do not use `@blocknote/server-util` in the Go server. It is a JavaScript
server-side conversion package, so adopting it would add a Node service or
subprocess merely to transform documents. Its documented purpose is server-side
HTML, Markdown and Yjs conversion
([server-side processing](https://www.blocknotejs.org/docs/features/server-processing)).

## The blocking question: Markdown fidelity

BlockNote's native, lossless format is its block JSON. Its docs recommend that
format for durable storage. Both Markdown directions are explicitly lossy.
The built-in parser covers headings, paragraphs, lists, task lists, tables,
code, blockquotes, links, images, emphasis, strikethrough and hard breaks. It
does not aim to support every Markdown dialect. Unknown syntax may become plain
text. Export can un-nest children of non-list blocks and remove styles
([format interoperability](https://www.blocknotejs.org/docs/foundations/supported-formats),
[Markdown import](https://www.blocknotejs.org/docs/features/import/markdown),
[Markdown export](https://www.blocknotejs.org/docs/features/export/markdown)).

That contract is not strong enough for Worklode. In particular, BlockNote does
not document preservation of:

- YAML frontmatter, which Worklode parses as document metadata and relations;
- Worklode's `{#sec-N}` heading suffixes, whose stable fragments identify
  sections;
- Mermaid fences, footnotes, definition lists, raw HTML or other extensions;
- the exact whitespace and ordering needed for a no-surprise whole-body edit.

This is not proof that every one of these is lost. It means the project makes
no preservation promise for them. A save path must treat them as unsafe until
fixtures show otherwise. Converting Markdown to HTML first, as the docs suggest
for richer imports, does not solve exact round-trip preservation; HTML
conversion is also described as lossy.

Storing BlockNote JSON alongside Markdown would avoid loss inside BlockNote,
but it would create two competing sources of truth and require migrations,
conflict rules, API changes and agent/CLI compatibility. Worklode already owns
Markdown as the document artifact. Do not add the second format unless the
product explicitly changes that rule.

## Features, dependencies and assets

The default schema includes ordinary text blocks, headings, lists, tables,
code, quotes and file/media blocks. Schemas can be extended or built from
scratch with custom blocks, inline content and styles
([built-in blocks](https://www.blocknotejs.org/docs/features/blocks),
[custom schemas](https://www.blocknotejs.org/docs/features/custom-schemas)).
Custom React blocks have hooks for HTML and external-format export, but each
Worklode-only construct would still need an import/export policy
([custom blocks](https://www.blocknotejs.org/docs/features/custom-schemas/custom-blocks)).
For the spike, preserve frontmatter outside the editor and avoid custom blocks.

The v0.54.0 core package is an ESM/CJS package built on ProseMirror and Tiptap.
Its manifest has many runtime dependencies and marks CSS as a side effect. The
React package accepts React 18 or 19; the Mantine view also requires Mantine 8
or 9 ([core manifest](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/packages/core/package.json),
[React manifest](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/packages/react/package.json),
[Mantine manifest](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/packages/mantine/package.json)).
The upstream repository currently develops with Node 24.15 and pnpm 11.8, but
the published package manifests declare no Node `engines` floor
([Node version](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/.node-version),
[root manifest](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/package.json)).

Worklode would need a JavaScript bundling step. BlockNote's core stylesheet is
required; its Inter font stylesheet is optional in practice if Worklode maps
the editor to its existing fonts, but that must be checked visually. React UI
packages add their own stylesheet. The cockpit embeds and serves versioned
assets itself and has `script-src 'self'`, `style-src 'self'` and
`font-src 'self'`, so checked-in built assets fit the current Content Security
Policy (CSP); a CDN build does not. Measure compressed JavaScript and CSS in
the spike rather than quoting a package-size badge, because the shipped size
depends on the chosen UI package and tree shaking.

The official docs do not publish a supported-browser matrix. The editor uses
modern browser APIs and is distributed as modern JavaScript. Treat current
Chrome, Firefox and Safari—including Worklode's narrow/mobile checks—as the
acceptance matrix, and verify them in the spike. Do not infer support for older
browsers from the absence of an `engines` or browsers field.

## Uploads, links and collaboration

BlockNote does not store uploads. The application supplies an `uploadFile`
function and receives a URL; `resolveFileUrl` can turn a stored reference into
an access URL ([file panel](https://www.blocknotejs.org/docs/react/components/image-toolbar),
[editor options](https://www.blocknotejs.org/docs/reference/editor/overview)).
This maps cleanly to Worklode's blob API, but the server must remain responsible
for authorization, size and media-type checks. The spike should disable upload
UI. Add it only after defining how a BlockNote media block serializes to the
exact Markdown reference the blob scanner already understands.

Links are block content with an arbitrary `href`, and the editor allows custom
link rendering and click handling
([inline content](https://www.blocknotejs.org/docs/features/blocks/inline-content)).
Do not rely on the editor as a trust boundary. Validate allowed schemes on
write or click, keep external links from gaining opener access, and continue to
sanitize rendered Markdown on the server.

Real-time editing uses Yjs plus a separately chosen transport and persistence
provider. The docs list hosted and self-hosted choices including Liveblocks,
PartyKit, Y-Sweet, Hocuspocus, y-websocket, IndexedDB and WebRTC
([collaboration guide](https://www.blocknotejs.org/docs/features/collaboration)).
Worklode has whole-body versioned writes, ownership rules and candidate
revisions, not a collaborative-operation store. Adding Yjs would therefore be
a separate product and data-model project. It is not needed to learn whether
BlockNote can safely edit one draft.

## Security consequences

BlockNote runs inside an authenticated cockpit page and turns stored content
into an editable DOM. Keep the existing server sanitizer for every read-only
render and preview. Do not persist editor-produced HTML. Persist Markdown only
after Worklode's existing parser and lint rules accept it.

The integration also needs these boundaries:

- bundle scripts, styles and fonts locally so the existing CSP stays
  `self`-only;
- keep CSRF protection, document permissions and ownership checks on the
  server, and add server-enforced optimistic concurrency; a hidden or disabled
  editor control is not access control;
- reject unsafe link schemes and avoid automatic remote embeds that leak the
  viewer's IP or cookies;
- keep upload validation and signed/private blob access in Worklode;
- preserve the textarea as a recovery path until round-trip behavior is proven.

BlockNote's pricing page says document data does not pass through BlockNote's
servers. That is true for the self-hosted editor itself, but stops being enough
once a hosted Yjs provider, AI endpoint or remote media URL is configured
([pricing and data statement](https://www.blocknotejs.org/pricing)).

## License and stability

Core, React and the standard UI packages are Mozilla Public License 2.0
(MPL-2.0). They can be used in closed-source applications; modifications to
covered BlockNote source files must be published under that license. The
`@blocknote/xl-*` packages are GPL-3.0 or commercially licensed. XL includes
AI, multi-column layouts and PDF, DOCX, ODT and email exporters
([repository license](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/LICENSE.txt),
[pricing](https://www.blocknotejs.org/pricing)). Worklode needs none of those
for a Markdown editor. Use only the MPL packages in the spike.

BlockNote is active, but its version is still `0.x`. Releases are frequent:
v0.47.2 through v0.54.0 shipped between March and August 2026. Recent minor
versions included a breaking Shadcn UI change and a Yjs integration migration.
Pin exact package versions and review the migration notes before upgrades
([releases](https://github.com/TypeCellOS/BlockNote/releases),
[changelog](https://github.com/TypeCellOS/BlockNote/blob/v0.54.0/CHANGELOG.md)).
The active maintenance is reassuring; the pre-1.0 surface and recent migrations
argue against making it the only editing path before fixture and browser tests
exist.

## Recommendation and smallest viable spike

**Proceed with a time-boxed compatibility spike; do not commit to integration
yet.** The decision gate is lossless-enough handling of Worklode Markdown, not
whether the editor looks good.

Build one opt-in draft-edit page with `@blocknote/core@0.54.0` only:

1. Fetch one draft through the existing document API. Split off its YAML
   frontmatter without editing it.
2. Parse only the Markdown body into BlockNote and mount it beside the existing
   textarea. Do not add uploads, collaboration, AI, XL packages or custom
   blocks.
3. On save, export Markdown, restore the untouched frontmatter, and send it
   through the existing validation path with a version precondition. Refuse
   the save if the document changed since it was loaded or if a parse/export
   round trip changes protected fixtures.
4. Test a small corpus containing every construct Worklode relies on:
   frontmatter, `{#sec-N}` headings, typed relations, nested lists, task lists,
   tables, fenced code with language, Mermaid, links, images, inline HTML and
   blank lines. Compare parsed Worklode metadata, sections and edges as well as
   text.
5. Record locally served gzip/Brotli asset sizes and run desktop plus narrow
   Chrome, Firefox and Safari checks. Verify keyboard use, focus, screen-reader
   labels, dark theme and no-JavaScript fallback.

Accept BlockNote for draft editing only if the protected corpus survives or a
small, explicit preservation layer handles the exceptions without introducing
BlockNote JSON as another source of truth. If section anchors or ordinary
Markdown constructs cannot survive, stop. The existing Markdown input is the
smaller and safer editor.
