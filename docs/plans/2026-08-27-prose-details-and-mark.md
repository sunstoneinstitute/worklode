---
status: draft
covers: NO-SPEC
---
# .prose styling for details/summary and mark

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:executing-plans (two small tasks; no fan-out needed). Steps
> use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `<details>`/`<summary>` and `<mark>` already survive mdrender's
sanitizer (`internal/mdrender/mdrender.go:145,149`) but render browser-bare
inside `.prose` because `internal/ui/styles/app.tailwind.css` has no rules
for them. Give both the design system's treatment — light and dark, on
existing tokens where they clear WCAG AA — regenerate the committed
`app.css`, and hold `<mark>` to the same contrast test the rest of the
palette answers to (`internal/ui/contrast_test.go`).

No governing spec (`NO-SPEC`): this is cockpit polish inside 032 §10's
standing contrast/theme rules, which `contrast_test.go` already encodes.

**Read first:**
- `internal/ui/styles/app.tailwind.css` — the `.prose` block (:538-566), the
  four theme token blocks (:27-:68; every colour declared in all four, per
  `TestEveryThemeIsDeclaredOnceInEachDirection`), and the `--warn`/`--warn-bg`
  pairs
- `internal/ui/contrast_test.go` — `textPairs` (:164) and
  `TestTextContrastMeetsAA`: adding a pair row is how a new colour use gets
  held to 4.5:1 in both themes
- `internal/mdrender/mdrender.go:140-150` — the allowlist that lets these
  elements through
- The `worklode-cockpit-ui` skill — the `go generate ./...` loop;
  `scripts/gen-web.sh` needs `./bin/tailwindcss` present

## Global constraints

- Both elements are styled inside `.prose` only — they reach the page
  exclusively through sanitized markdown, and nothing outside `.prose`
  renders them.
- Colours come from tokens declared in all four theme blocks; no literal
  hex in the rules. New tokens (only if needed — see Task 1) are declared
  in all four blocks or `TestEveryThemeIsDeclaredOnceInEachDirection`
  fails, which is correct.
- `go generate ./...` regenerates `internal/ui/assets/app.css`;
  commit the generated stylesheet with the source change.
- Every task leaves `go test -trimpath ./internal/ui -count=1` green and
  ends in its own commit.

## Tasks

### Task 1 — `.prose mark` on the warn pair, held to AA

```yaml
kind: chore
priority: low
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [ ]
```

Test first: add to `textPairs` in `internal/ui/contrast_test.go`:

```go
{"ink", "warn-bg", ".prose mark — highlighted text in a task body"},
```

Run `go test -trimpath ./internal/ui -run TestTextContrastMeetsAA -count=1`.
Two outcomes, both fine:

- **Green**: `--ink` on `--warn-bg` clears 4.5:1 in both themes — style
  `<mark>` on the existing pair, no new token:

```css
.prose mark{background:var(--warn-bg);color:var(--ink);border-radius:3px;padding:0 3px}
```

- **Red in either theme**: mint `--mark-bg` in all four theme blocks — the
  light one starting from `#FBEBDA` and the dark from `#3A2A16`, adjusted
  until `--ink` on it clears 4.5:1 in that theme — change the pair row and
  the rule to `--mark-bg`, and keep the failing-then-passing test as the
  record of why the token exists.

Either way `<mark>` must not rely on colour alone for meaning it doesn't
carry — it is emphasis, and the padding/radius make it visibly a highlight
in forced-colors/greyscale too.

- [ ] Add the textPairs row; run the contrast test and take the branch it
      dictates
- [ ] Add the `.prose mark` rule; `go generate ./...`
- [ ] `go test -trimpath ./internal/ui -count=1` → ok
- [ ] Visual check on the compose stack: a task body with `<mark>` shows the
      highlight in both themes
- [ ] Commit including `internal/ui/assets/app.css`

### Task 2 — `.prose details` and `.prose summary`

```yaml
kind: chore
priority: low
skills:
  - worklode-cockpit-ui
blockedBy: [ ]
```

Disclosure styling on existing tokens only — no contrast dimension beyond
what the tokens already guarantee (`--ink` on `--surface-2` is the `.prose
pre` pair, already in service):

```css
.prose details{border:1px solid var(--line);border-radius:9px;padding:9px 13px;margin:0 0 12px;background:var(--surface-2)}
.prose details[open] summary{margin-bottom:8px}
.prose summary{cursor:pointer;font-weight:600}
```

The native disclosure marker stays (it is the affordance); the global
`:focus-visible` outline already covers keyboard focus on `<summary>`, so
add nothing focus-specific. Spacing follows the `.prose pre` card idiom so
a collapsed details reads as one bordered row and an open one as a card.

A small render regression: extend the existing `.prose`/task-body render
test (or `internal/ui/views_test.go`'s nearest fixture) with a body
containing `<details><summary>More</summary>hidden</details>` and
`<mark>hot</mark>`, asserting both elements survive to the rendered page —
pinning the sanitizer allowlist and the stylesheet's subjects together, so
a future allowlist change that would orphan these rules fails a test.

- [ ] Add the CSS; `go generate ./...`
- [ ] Extend the render test; `go test -trimpath ./internal/ui ./internal/mdrender -count=1` → ok
- [ ] Visual check in both themes: collapsed and open states
- [ ] Commit including `internal/ui/assets/app.css`
