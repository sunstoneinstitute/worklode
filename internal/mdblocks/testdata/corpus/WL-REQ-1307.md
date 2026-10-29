
The [SPA](/projects/worklode/term/spa) calls `/api/v1` on its own origin. There is no [CORS](/projects/worklode/term/cors).

`/api/v1` routes accept either a bearer token or the web session cookie, the
way `router.asset` does today. A request carrying the session cookie is
authorized as the session's subject under the same `routeGuards` permission
as a bearer request.

Cockpit acts, derived views and event streams that exist only as templ
routes today get `/api/v1` routes when the page that uses them is ported
(WL-REQ-1305).
