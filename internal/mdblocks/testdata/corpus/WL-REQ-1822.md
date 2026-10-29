
lode-server serves MCP over Streamable HTTP at `/mcp`, built on `modelcontextprotocol/go-sdk` in stateless mode (`StreamableHTTPOptions.Stateless`): the server neither sets nor reads `Mcp-Session-Id`, so any replica serves any request. It keeps no state per client or per conversation: a conversation's project travels on each call as a signed handle (WL-REQ-1828). Version 1 offers tools only: no resources and no prompts.

`/mcp` takes a bearer `wl_` token, the same token the API takes. `/mcp`, `/oauth/*` and `/.well-known/oauth-*` each have a `routeGuards` row.
