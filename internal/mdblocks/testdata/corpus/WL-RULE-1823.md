
A tool reaches data only by calling an `/api/v1` handler in-process through `cli.Client`, with the caller's own `wl_` token. No MCP code reads or writes the store directly or makes its own permission decision, so route guards, `Decide`, validation and API metrics apply exactly as they do for the CLI.

Text output reuses the `cli.*Render` functions. `show` and `lode show` share `cli.DocShowText` and `cli.RuleFetcher`, and inline folding runs `internal/designdoc` on the server.

Why: one permission model. MCP cannot grant what the API refuses.

