
One [OIDC](/projects/worklode/term/oidc) login serves both surfaces, with one callback
(`/auth/callback` on `LODE_PUBLIC_URL`) registered with the identity
provider. Session cookies stay host-only: no `Domain` attribute, `HttpOnly`,
`Secure`, `SameSite=Lax` (WL-REQ-334). A templ page and an SPA page share
the session, and the origin checks in `webform.go` compare against
`LODE_PUBLIC_URL`.
