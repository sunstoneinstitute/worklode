
One static public client, configured in Worklode. No dynamic client registration.

| Client id      | Redirect URI                              | Used by           |
|----------------|-------------------------------------------|-------------------|
| `worklode-mcp` | `http://localhost:33418/callback`         | Claude Code       |
|                | `http://127.0.0.1:33418/callback`         | Claude Code       |
|                | `https://claude.ai/api/mcp/auth_callback` | claude.ai, Cowork |

Port 33418 is the one `grafana-mcp` already uses.

