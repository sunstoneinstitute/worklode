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
