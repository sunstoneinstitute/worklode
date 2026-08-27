---
status: draft
covers: NO-SPEC
---
# Callout blocks in cockpit markdown

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** GitHub alert syntax (`> [!NOTE]` and friends) renders as a styled
callout in every cockpit markdown surface instead of a literal blockquote.
Per WL-350's design decisions (settled; do not reopen): a small custom
Goldmark extension rather than a hobby-repo dependency; a narrow, explicit
bluemonday allowlist addition with its own guard tests, because
`buildPolicy()` deliberately strips `class` globally and that boundary stays
deliberate; and the five GitHub kinds — `note`, `tip`, `important`,
`warning`, `caution` — styled in both themes and held to WCAG AA by
`internal/ui/contrast_test.go`. The CSS/contrast work is most of the effort,
not the parser. The corpus has zero admonition usage today, so nothing
migrates.

`covers: NO-SPEC` — a render-path feature under 032 §10's standing contrast
rules; no spec governs markdown flavour.

**Read first:**
- `internal/mdrender/mdrender.go` — `md` and `mdDoc` (both get the
  extension: task bodies and doc bodies), `emojiExt`/`mermaidExt` (the
  extension-registration pattern), `buildPolicy` (:151, built from empty —
  never from UGCPolicy) and the `sectionAnchor` id rule (:249), the one
  attribute precedent this plan's `class` rule follows
- `internal/mdrender/mdrender_test.go` — `TestHostileBodies`,
  `TestPolicyIsNotUGCPolicy`, `TestSafeMarkupSurvives`: the guard suite the
  new policy surface must join
- `internal/mdrender/cache.go` — in-process only, keyed by body hash: a
  deploy with the new renderer starts cold, so no cache-version concern
- `internal/ui/styles/app.tailwind.css` — the four theme token blocks
  (:27-:68), `--ok`/`--warn` and their `-bg` pairs, the `.prose` block
- `internal/ui/contrast_test.go` — `textPairs` and
  `TestEveryThemeIsDeclaredOnceInEachDirection`: how a new colour use gets
  held to 4.5:1 and how a new token must appear in all four blocks

## Global constraints

- **Exact markup, pinned.** The extension turns a blockquote whose first
  paragraph begins with `[!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]`,
  or `[!CAUTION]` (its own line, GitHub's grammar) into:

  ```html
  <aside class="callout callout-note">
    <p class="callout-title">Note</p>
    ...the blockquote's remaining children, rendered as normal...
  </aside>
  ```

  Title words are exactly `Note`, `Tip`, `Important`, `Warning`, `Caution`.
  A blockquote that does not match — unknown kind (`[!FOO]`), marker not
  first, marker mid-paragraph — renders as an ordinary blockquote,
  byte-for-byte what today produces.
- **The policy addition is exactly two rules**, mirroring the
  `sectionAnchor` id precedent (anchored regexps, one element each):
  `class` matching `\Acallout callout-(note|tip|important|warning|caution)\z`
  on `aside`, and `class` matching `\Acallout-title\z` on `p`. No other
  element gains `class`; nothing gains `style`. The page CSP is untouched.
- **Colour tokens:** `warning` reuses `--warn`; `tip` reuses `--ok`. Three
  new foreground tokens are minted in all four theme blocks: `--info`
  (note, blue range), `--imp` (important, purple range), `--danger`
  (caution, red range). Each is held to 4.5:1 against both `--surface` and
  `--surface-2` in both themes via new `textPairs` rows — the test picks
  the final values, not this plan. Callout bodies stay `--ink` on the
  existing surfaces; the kind is carried by the title word and the coloured
  title/border, never by colour alone.
- **Both pipelines** (`md`, `mdDoc`) register the extension, so task bodies,
  doc bodies, and every other `.prose` consumer behave the same.
- **Every task leaves `go test -trimpath ./...` green**, regenerates and
  commits `internal/ui/assets/app.css` when the stylesheet changes, and
  ends in its own commit.

## Tasks

### Task 1 — The Goldmark callout extension

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

New `internal/mdrender/callout.go` (~120 lines): an
`parser.ASTTransformer` that walks blockquotes, matches the first
paragraph's leading `[!KIND]` line against the five kinds, and replaces the
node with a custom `calloutNode` (kind + the blockquote's remaining
children, the marker line removed); plus a `renderer.NodeRenderer` emitting
the pinned markup. Registered on `md` and `mdDoc` as a
`goldmark.Extender` beside `emojiExt`.

Parsing details pinned: the marker must be the entire first line of the
first paragraph (GitHub's rule); text after the marker on the same line
disqualifies; matching is case-insensitive on input (`[!note]` works) but
the emitted class and title use the canonical spellings.

First tests, `internal/mdrender/callout_test.go`, table-driven over
`Body(...)` output (the sanitizer is part of the assertion — Task 2 lands
first if the pair programs in either order; if this task runs first, assert
the pre-sanitizer renderer output via the package-internal render, and let
Task 2's tests own the end-to-end survival):

- each of the five kinds produces its aside + title + body;
- `[!FOO]`, marker-with-trailing-text, and a marker not in the first
  paragraph all fall through to today's `<blockquote>` output unchanged;
- nested markdown inside a callout (list, code fence, link) renders as it
  does inside a blockquote;
- a callout inside a callout renders the outer one and leaves the inner
  blockquote alone only if it, too, matches — same rules, recursively.

- [ ] Write the tests; watch them fail
- [ ] Implement `callout.go`; register on both pipelines
- [ ] `go test -trimpath ./internal/mdrender -count=1` → ok
- [ ] Commit

### Task 2 — The policy allowlist addition, guarded

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

`internal/mdrender/mdrender.go` `buildPolicy` (and `buildDocPolicy` if it
does not inherit — read it first): add `aside` to the allowed elements and
the two anchored `class` rules from Global constraints, each with a comment
naming this plan and the reason class stays otherwise stripped. This is a
security-boundary edit; the tests are the point:

- extend `TestHostileBodies` with the smuggling attempts this rule could
  invite: raw `<aside class="callout callout-note" onclick="x">` (attribute
  stripped, element kept), `<aside class="evil">` (class stripped),
  `<p class="callout-title">` typed raw in a body (allowed — it is inert
  styling, same stance as the sanitizer's other cosmetic survivals; assert
  that stance explicitly so it is a decision, not an accident),
  `<div class="callout">` (class stripped — the rule is per-element),
  `<aside><script>` (script gone as ever);
- extend `TestSafeMarkupSurvives` with the end-to-end case: the `[!NOTE]`
  markdown renders through `Body()` with the aside, both classes, and the
  title intact;
- a `TestCalloutClassPattern` pinning the regexps reject
  `callout callout-note extra`, `callout`, `callout-note`, and uppercase.

- [ ] Write the guard tests; watch the survival case fail
- [ ] Add the policy rules
- [ ] `go test -trimpath ./internal/mdrender -count=1` → ok
- [ ] Commit

### Task 3 — Styling, tokens, and the AA gate

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [2]
```

The bulk of the work, test-first:

1. `internal/ui/contrast_test.go`: add `textPairs` rows for every
   title-colour/surface combination — `ok`, `warn`, `info`, `imp`,
   `danger`, each against `surface` and `surface-2`, in both themes (the
   table is theme-agnostic; two rows per token). Red until the tokens
   exist and clear.
2. `internal/ui/styles/app.tailwind.css`: mint `--info`, `--imp`,
   `--danger` in all four theme blocks (values chosen to pass step 1 —
   iterate against the test, the same loop the §10 audit tokens went
   through), then the component rules in the `.prose` section:

   ```css
   .prose .callout{border:1px solid var(--line);border-left:3px solid var(--callout-c,var(--line-2));border-radius:9px;padding:9px 13px;margin:0 0 12px;background:var(--surface-2)}
   .prose .callout-title{font-weight:600;margin:0 0 6px;color:var(--callout-c)}
   .prose .callout-note{--callout-c:var(--info)}
   .prose .callout-tip{--callout-c:var(--ok)}
   .prose .callout-important{--callout-c:var(--imp)}
   .prose .callout-warning{--callout-c:var(--warn)}
   .prose .callout-caution{--callout-c:var(--danger)}
   ```

   (The `--callout-c` indirection keeps it to one rule set; adjust only if
   the Tailwind CLI chokes on it.)
3. Render regression in `internal/ui` (or wherever the `.prose` fixture
   test from the details/mark plan lands — coordinate, don't duplicate): a
   body with one callout renders the aside and title through the full
   page path.
4. Visual check on the compose stack: all five kinds, both themes, plus a
   plain blockquote beside them to confirm it still reads as one.

- [ ] textPairs rows first; watch them fail on the missing tokens
- [ ] Tokens + rules; `go generate ./...`; iterate until the contrast test
      passes in both themes
- [ ] `go test -trimpath ./internal/ui ./internal/mdrender -count=1` → ok
- [ ] Visual check; commit including `internal/ui/assets/app.css`
