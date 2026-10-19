# MCP connector reachability

Where MCP clients reach lode-server, per environment (WL-REQ-1841).

| Env | MCP base URL | Who uses it |
|----|----|----|
| dev | `https://worklode.dev.sunstoneinstitute.ai` | all clients (one origin, no `LODE_MCP_PUBLIC_URL`) |
| prod | `https://mcp.sunstoneinstitute.ai/worklode` | claude.ai, Cowork |
| prod | `https://worklode.sunstone.internal` | Claude Code (mesh) |

## Prod public ingress

`deploy/overlays/hzprod/mcp-public-ingress.yaml` (class `nginx`, host
`mcp.sunstoneinstitute.ai`) exposes four paths and nothing else:

| Public path | lode-server path |
|----|----|
| `/worklode/mcp` | `/mcp` |
| `/worklode/oauth/token` | `/oauth/token` |
| `/.well-known/oauth-protected-resource/worklode/mcp` | `/.well-known/oauth-protected-resource/mcp` |
| `/.well-known/oauth-authorization-server/worklode` | `/.well-known/oauth-authorization-server` |

The overlay sets `LODE_MCP_PUBLIC_URL=https://mcp.sunstoneinstitute.ai/worklode`.
The Host header is passed through, and lode-server picks the origin by it.
Proxy buffering is off and read/send timeouts are one hour so SSE streams
stay open.

## Allowlist

The ingress carries `whitelist-source-range: 160.79.104.0/21`, Anthropic's
published outbound range.

The range matches the real caller because the hzprod load balancer uses proxy
protocol and ingress-nginx runs with `use-proxy-protocol: "true"` and
`use-forwarded-headers: "false"`. A client-supplied `X-Forwarded-For` is
ignored. This is set in the provisioning repo,
`clusters/hzprod/ingress-nginx/kustomization.yaml`, not here. If that
configuration changes, the allowlist stops being a security boundary.

## Browser leg

`/oauth/authorize` and `/auth/callback` stay on the mesh host
`worklode.sunstone.internal`, so the user's device must be on the mesh when
connecting.
