
Every route is listed in `internal/api/router.go`'s `routeGuards` with the permission it requires, or `open("why")` when it deliberately needs no Worklode identity. `NewServer` refuses to boot when a route is missing from the table or a table entry names no route, so an endpoint cannot ship unguarded. Cockpit page-script writes pass one more gate, `beginJSONPost` (same-origin, `X-Requested-With`, JSON body, actor from the session); see WL-REQ-334.

The open routes are `/hooks/*` (authenticated by HMAC signature), `/.well-known/lode-login`, and the `/auth/*` login routes. `/healthz` and `/metrics` are served on a separate admin listener that the public ingress never routes to and that has no authentication middleware, so they are outside the guard table.

