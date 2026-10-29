
`GET /auth/oidc/config` and `POST /auth/oidc/token` serve a client that holds its own Keycloak ID token. `POST /auth/oidc/token` with body `{"id_token": "..."}` runs section 4.3 and mints a 30-day token with description `sso login for <user> at <RFC3339>`, returning `{"token": ...}` once. It returns 404 when OIDC is unconfigured. There are no refresh tokens; the user logs in again after expiry.

