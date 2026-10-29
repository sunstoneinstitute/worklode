
Styling is Tailwind CSS v4 through the standalone CLI (CSS-first `@theme`, no
PostCSS). The approved palette and typography are `@theme` tokens. Variable
fonts are self-hosted. Light and dark key on `prefers-color-scheme` with a
`[data-theme]` override. Shell rules that are not utility-shaped (skip link,
visible focus, minimum target size, the narrow-viewport column) are
hand-authored CSS. HTMX is embedded and served from the app's own assets as the
interactivity substrate; no template uses HTMX writes. No third-party CDN
enters a served page, and Node.js enters neither the templ pages' build nor
any served page (the SPA's build is WL-REQ-1310). Generated artifacts
(`*_templ.go`, the built stylesheet) are committed and marked generated, so
`go build` and `go test` need none of the tools. CI regenerates them and fails
on drift (`scripts/check-generated.sh`).

