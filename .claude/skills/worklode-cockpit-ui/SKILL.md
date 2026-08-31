---
name: worklode-cockpit-ui
description: Use when changing the web cockpit UI in internal/ui — "the cockpit", "the web page", "a templ component", "*_templ.go", "templ generate", "tailwind", "app.css", "the stylesheet", "restyle", "add a column to the page", "go generate", "LODE_WEB_OPEN". Covers the templ + Tailwind dev loop and the ui/api dependency direction. Not for the JSON API — that is internal/api plus internal/model.
---

# Cockpit UI development

`internal/ui` owns the cockpit's page components and the design-system assets
(stylesheet, fonts, htmx) served at `/assets/` via `internal/api`'s
`assetHandler`. The components take `internal/ui` view types (page-shell state
and pre-formatted strings, composing `internal/model` types) that
`internal/api`'s `render.go` builds.

`internal/ui` depends on nothing beyond stdlib, `internal/model`, and the templ
runtime — `internal/api` imports `internal/ui`, never the reverse.

**Editing a `.templ` file is only half the change.** `*_templ.go` and
`internal/ui/assets/app.css` are committed build artifacts — regenerate them
with `go generate ./...` (or the watchers below) and commit the result, or the
page you see will not match the source you edited.

Pages render with `templ` components (`internal/ui/*.templ`, compiled to
`*_templ.go` by `go generate`), styled by a standalone Tailwind CSS v4 build
(`internal/ui/styles/app.tailwind.css` → `internal/ui/assets/app.css`) and a
self-hosted, currently dormant HTMX. Both generated artifacts are committed.

## Authoring rules

**The data path.** `ui` renders view types and nothing else: `store row ->
api DTO -> render.go mapper -> views.go type -> *.templ`. Adding a field
means touching each link. A component never queries: if a template needs a
fact it lacks, the fix is a field on the view type and a line in
`render.go` — never an import of `internal/api` (a dependency cycle) and
never a store call from a template. View types embed `internal/model`
types only (ADR 036) — never `internal/store` ones — matching the
dependency line above.

**Honest empty states** — the rule the cockpit is built on, and the
easiest to break by being helpful. A panel whose data is absent is
omitted, not filled with a placeholder value. Never fabricate a count,
status or record; where the backbone stores no fact the page says so
(`cockpit.templ`'s `automationBoundary` renders "not configured" for the
policy/budget the backbone doesn't have yet, and "none recorded" for no
spend). An empty list gets an explicit line ("No active work.", "None"),
never a silently empty container. If a design asks for something the store
cannot answer, the answer is a `ui.PlaceholderView` naming the owning spec
section (built in `render.go`), not an invented value.

**When a new component is warranted.** Add one when the markup repeats
across files, or when a template body no longer reads as a page outline.
Two or three uses in one file is a local `templ` helper in that file (e.g.
`boardBucket` in `board.templ`). Do not add one for a single wrapper with
no logic. Lowercase the name unless `internal/api` renders it directly;
exported names (`Task`, `RunBoard`, `Home`, ...) are the page entry
points. A `switch` mapping a domain value to a class or label belongs in
`views.go`/`ui.go`, not inline in a template.

**Accessibility invariants.** Exactly one `aria-current="page"` per page:
project-scoped pages leave `PageProps.ActiveGlobal` empty so the local nav
carries it instead, and getting that wrong — zero, or two — is what
`internal/api/web_test.go`'s `assertOneAriaCurrent`/`assertNoAriaCurrent`
catch. The shell owns the skip link and `<main id="main-content">`; pages
never emit a second `<main>`. Decorative SVG carries `aria-hidden="true"`;
an icon that conveys meaning needs a label. Section landmarks take
`aria-labelledby` pointing at their own heading id.

**The `SafeURL` trap.** templ's sanitizer turns an unsafe scheme into
`about:invalid#TemplFailedSanitizationURL` when a plain string is
interpolated into `href`. That is why `TimelineRow.URL` (`task.templ`) is
rendered as a string and not pre-wrapped in `templ.SafeURL` — wrapping it
yourself asserts it is safe and skips the sanitizer
(`TestTaskPageEscapesHostileTimelineURL` in `internal/api/web_test.go`
pins it). Nav labels are emitted tight against their tags (`>Ideas<`, not
`>\n  Ideas\n<`) so `internal/api/web_test.go`'s `assertOrder` and
`>Home<`-style checks can match exact text markers — keep new nav markup
to that convention.

## Dev loop

Driven by the single `//go:generate` directive in `internal/ui/ui.go`:

```bash
go tool templ generate --watch        # regenerate *_templ.go on change
./bin/tailwindcss -i internal/ui/styles/app.tailwind.css \
  -o internal/ui/assets/app.css --watch
./scripts/fetch-tailwind.sh           # one-time: install the pinned CLI into bin/
go generate ./...                     # regenerate both committed artifacts
```

## Before merging a new or reshaped page

```bash
./scripts/narrow-check.sh    # measure every page at 320/375/640/768 CSS px
```

It renders each page component with the fixtures in `internal/ui/narrow_test.go`
(no Postgres, no server — `internal/ui` renders standalone), serves them with the
real stylesheet, and measures horizontal overflow, clipped text, pointer-target
size and focus obscuring in a headless browser. **A new page is only measured
once it is added to that fixture map**, and the fixtures are deliberately long —
a fixture reading `Title: "t"` reflows perfectly and proves nothing.

The check needs a Chrome-family browser and finds one on PATH or in a
Playwright/Puppeteer cache; `LODE_NARROW_BROWSER` overrides. With none on the
machine it says so and exits 0 — CI does not install a browser (spec 032 §12),
so `internal/ui/narrow_test.go` pins each fix it has produced as a stylesheet or
markup fact instead, and that is what `make test` runs.

The cockpit is session-gated and read-mostly. Development mode is open only
when `LODE_WEB_OPEN` is set and no login provider is configured; that path
serves the anonymous `authOpen` subject. Route permissions still come from
`routeGuards` — see the Architecture section of CLAUDE.md.
