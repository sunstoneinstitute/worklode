
The [SPA](/projects/worklode/term/spa)'s build output is embedded in the binary with `go:embed`.

- Its files are built under `static/` and served from `/static/` with
  `Cache-Control: public, max-age=31536000, immutable`. Their names carry a
  content hash. A missing file is a 404, never `index.html`. `/assets/`
  belongs to the templ cockpit.
- A GET for a path the SPA handles (WL-REQ-1305) returns `index.html` with
  `Cache-Control: no-cache`.

`index.html` is served with the same `Content-Security-Policy` as the cockpit
(`frame-ancestors 'none'`, `script-src 'self'`). The SPA's build emits no
inline script.
