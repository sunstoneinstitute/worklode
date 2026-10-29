
Worklode is the OAuth authorization server for MCP clients. Keycloak stays upstream, through the existing `worklode-<env>` client and `/auth/callback`. No Keycloak realm change.

- `GET /.well-known/oauth-protected-resource[/mcp]` serves resource metadata (RFC 9728). `/mcp` without a valid token answers 401 with `WWW-Authenticate: Bearer resource_metadata=...`.
- `GET /.well-known/oauth-authorization-server` serves authorization-server metadata (RFC 8414), per origin. PKCE (Proof Key for Code Exchange) with S256 only.

