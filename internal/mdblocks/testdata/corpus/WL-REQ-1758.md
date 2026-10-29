
Form writes pass `internal/api/webform.go`'s `sameOriginForm`, which refuses a
POST whose `Sec-Fetch-Site` header says `same-site` or `cross-site`, or, when
the header is absent, whose `Origin` host differs from the request host and the
configured public URL. Page-script writes (the Progress page's acts and the
task and document pages' Accept and Publish buttons) pass `beginJSONPost`
instead: POST only, `Sec-Fetch-Site` exactly `same-origin` when present, the
header `X-Requested-With: lode-cockpit`, an `application/json` body of at most
64 KiB, and the actor taken from the session, so a body naming an actor is
refused. Its replies are JSON, never HTML. WL-REQ-1338 lists its status
codes. `Content-Security-Policy` sets `frame-ancestors 'none'` and
`script-src 'self'`. No CSRF token exists.

