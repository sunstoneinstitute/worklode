
`GET /oauth/authorize` checks `client_id`, `redirect_uri`, `code_challenge` and `resource`. It sets a signed MCP-intent cookie carrying a nonce, then runs the normal Keycloak login with that nonce in its signed OAuth state.

`finishLogin` takes its MCP branch only when the intent's nonce is that login's. The CLI login intent is bound the same way. A plain `/auth/login` clears the intent.

Why: an intent cookie left by an abandoned connector setup or `lode login` cannot turn a later plain web login into a token for someone else.

