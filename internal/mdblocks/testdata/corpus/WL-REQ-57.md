
1. `GET /.well-known/lode-login`.
2. Bind a loopback listener on an ephemeral port (`localhost:0`).
3. Open the browser at `{authorize_url}?redirect_uri=http://localhost:PORT/&state=CLISTATE`. The server runs its Keycloak login, reusing any existing browser session, provisions the actor, mints a one-time code and redirects to the loopback URI. Because the server performs that last redirect, the loopback URI needs no registration with Keycloak.
4. The listener receives `?code=…&state=…`; the CLI checks `state`.
5. `POST {token_url}`.
6. Store the token (section 8) and write only `server` to `config.toml`.

