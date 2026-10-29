
`POST /oauth/token` takes a form-encoded body. It checks the PKCE verifier and that `resource` is one of this server's MCP URLs, then returns an ordinary Worklode API token (an opaque `wl_` bearer token, the kind `lode login` gets) valid for 30 days (`ssoTokenTTL`), described as `mcp: <client>`. There is no refresh token: the user connects again after 30 days.

