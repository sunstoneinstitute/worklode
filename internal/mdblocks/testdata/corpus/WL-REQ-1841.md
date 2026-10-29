
MCP clients reach lode-server at one base URL per environment, as the hosts table in this rule lists. `LODE_PUBLIC_URL` stays the host the browser uses. `LODE_MCP_PUBLIC_URL` names a second base URL for MCP clients. Both are accepted as `resource`. The MCP base URL may carry a path prefix, as prod's `https://mcp.sunstoneinstitute.ai/worklode` does: the ingress strips the prefix, and lode-server writes it back into every URL it advertises for that origin (issuer, endpoints, `resource`, `resource_metadata`). `LODE_MCP_PUBLIC_URL` needs OIDC: the server refuses to boot with it set and OIDC off, because the MCP login runs through OIDC.

| Env | Host for /mcp | Who uses it |
|----|----|----|
| dev | `worklode.dev.sunstoneinstitute.ai` (public) | all clients |
| prod | `mcp.sunstoneinstitute.ai/worklode` (public, MCP paths only) | claude.ai, Cowork |
| prod | `worklode.sunstone.internal` (mesh) | Claude Code |

The prod public ingress (`deploy/overlays/hzprod/mcp-public-ingress.yaml`, class `nginx`) serves host `mcp.sunstoneinstitute.ai`. It exposes only `/worklode/mcp` and `/worklode/oauth/token`, stripping `/worklode`, plus the root metadata paths RFC 9728 and RFC 8414 derive for that prefix (`/.well-known/oauth-protected-resource/worklode/mcp`, `/.well-known/oauth-authorization-server/worklode`), each rewritten to lode-server's own `/.well-known/` path. It uses SSE-friendly proxy settings, and accepts only Anthropic's published outbound range `160.79.104.0/21`.

The browser leg (`/oauth/authorize`, `/auth/callback`) stays on the mesh host. The authorization-server metadata names `https://worklode.sunstone.internal/oauth/authorize` as `authorization_endpoint`, so the login reuses Keycloak's registered internal callback, and the user's device must be on the mesh when connecting.

Why the allowlist holds: the hzprod load balancer uses proxy protocol and the controller ignores forwarded headers, so the source range matches the real caller and an `X-Forwarded-For` header cannot spoof it.
