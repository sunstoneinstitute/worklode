# web/editor — WL-855 spike: BlockNote as a React island

A prototype, not a feature. It answers three questions about putting
[BlockNote](https://www.blocknotejs.org/) in the cockpit and stops there:
nothing saves, and no other page has an editor.

Reach it at `/docs/{ref}?editor=1`, which swaps the rendered body for the
island (`internal/ui/docs.templ`'s `docEditor`).

## Build

```bash
cd web/editor && pnpm install && pnpm build
```

esbuild writes two files straight into `internal/ui/assets/`, which the Go
binary embeds: `editor.js` (1.6 MB) and `editor.css` (235 kB, extracted from
the stylesheet the bundle imports). Both are committed, the way `app.css` and
`mermaid.min.js` are. No CI job builds this subtree.

The island takes its input from markup, because the cockpit has no inline
script to hand it through: `#doc-source` is a hidden `<textarea>` carrying the
stored markdown, `#doc-editor` is the element the React root mounts on.

## Check

```bash
./scripts/editor-check.sh
```

Renders the page, serves it under the cockpit's real CSP, and measures the
island in a headless browser. Needs Chrome, needs no Postgres.
`LODE_EDITOR_ROUNDTRIP_OUT=/path/out.md` also writes the exported markdown.

## What the spike found

**CSP.** The island mounts and stays styled under `script-src 'self';
style-src 'self'` with no nonce, but only because `src/cspstyles.ts` is there.
Three of BlockNote's dependencies inject a `<style>` element at runtime —
prosemirror-view (1.3 kB), Mantine's responsive helpers (0.8 kB), and one
Mantine placeholder that stays empty — and the browser refuses all three. A
refused `<style>` keeps its text and never gets a `.sheet`, so the shim copies
each one into a constructed stylesheet, which `style-src` does not police. It
is the same move `internal/ui/assets/mermaid-init.js` makes (WL-851). The
violations are still reported; the rules still apply.

BlockNote sets no `style` attributes, so `style-src`'s attribute half needs
nothing.

**The markdown round trip is lossy enough to block a save.** Measured on a
real document body (`internal/ui/testdata/editor-roundtrip.md`, the first three
sections of EA-SPEC-2), in the order that matters:

1. **Hard-wrapped prose is destroyed.** Every corpus document is wrapped at
   ~78 columns. BlockNote treats each newline inside a paragraph as a hard
   break and exports it as a trailing `\` plus a leading space on the next
   line. A save would rewrite every paragraph of every document it touched.
2. **Emphasis spanning a wrap is split.** `**fetch a URL and put the bytes\nin
   object storage**` comes back as `**…the bytes**\n** in object storage**` —
   two runs, with the space inside the markers.
3. **Frontmatter does not survive.** The `---` block is parsed as a thematic
   break, paragraphs and bullet lists. The `amends:` map is unrecoverable.
4. **Ordered lists lose their numbering and their multi-line items.**
   `1. 2. 3. 4.` all come back as `1.`, and a wrapped list item's continuation
   lines are lifted out of the list into their own paragraph.
5. **Blockquote continuation lines** pick up the same `\` and leading space.

Kept intact: `{#sec-N}` heading anchors (they are literal text in the heading),
fenced code blocks including the ` ```mermaid ` fence with its language tag,
heading levels and count, inline code, and em dashes.

**So the next step is not saving.** Points 1, 2 and 4 are all the same defect —
BlockNote's markdown serializer has no notion of soft wrapping — and they make
a naive save rewrite documents nobody edited. Anything built on this needs
either a serializer that re-wraps and treats a soft newline as a space, or a
save path that diffs at block level and writes back only the blocks the editor
actually changed.
