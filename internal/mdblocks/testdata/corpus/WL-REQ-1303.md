
`lode-server` serves the [SPA](/projects/worklode/term/spa) and the templ cockpit from one host,
`LODE_PUBLIC_URL`. A path the SPA handles (WL-REQ-1305) is registered to the
SPA shell. Every other cockpit path keeps its templ route. No path is
registered to both: porting a page deletes the templ route it replaces in
the same change, and `ServeMux` refuses to boot on a pattern registered
twice. `/api/v1`, webhooks, CLI login, `/auth` and `/blob` serve both
surfaces and are registered once. `/healthz` and `/metrics` stay on the
admin listener.

Every route keeps its `routeGuards` entry (WL-REQ-334).
