
`lode login` is provider-neutral and server-mediated. The CLI speaks no provider protocol and never sees a provider token. It opens a URL and waits.

| Endpoint | Purpose |
|---|---|
| `GET /.well-known/lode-login` | Discovery. Returns `{authorize_url: {public}/auth/cli/login, token_url: {public}/auth/cli/token, providers: ["keycloak"]}`. 404 when no interactive provider is configured; the CLI then tells the user to ask an admin for a token. |
| `GET /auth/cli/login?redirect_uri=…&state=…` | Validates `redirect_uri` is loopback only (`localhost`, `127.0.0.1` or `::1`, scheme `http`, explicit non-zero port), stores `{redirect_uri, state, mode}` in a short-lived cookie signed with the session secret, and redirects into the normal web login. With `mode=manual`, `redirect_uri` is omitted and `state` is still required. Any other combination is 400. |
| `POST /auth/cli/token` `{code, state}` | Validates the one-time code (exists, unexpired, unused, `state` matches the value bound at mint), mints a 30-day token with description `lode login`, marks the code used, returns `{token, actor_id, expires_at}`. Unauthenticated: holding the code is proof the browser flow completed. |

