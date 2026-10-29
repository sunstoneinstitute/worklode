
`lode channel serve` is a local stdio MCP relay that polls the backbone and delivers `lode task instruct` rows into the live sandbox session. It is registered by an `.mcp.json` entry in the checkout plus two launch flags, not at image build.

