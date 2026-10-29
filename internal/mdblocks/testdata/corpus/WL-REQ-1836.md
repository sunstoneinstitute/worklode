
`resource` is required at `/oauth/authorize` when two MCP origins are configured, as on prod. Only it says which issuer the client discovered, and RFC 9207 requires a correct `iss` on every response once the metadata advertises it. With one origin it may be left out. The MCP authorization spec already requires clients to send it.

