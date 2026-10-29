
lode-server runs an MCP (Model Context Protocol) server. It lets people manage Worklode tasks, documents and rules from Claude Code, claude.ai and Cowork without the `lode` CLI. It must work in voice conversations as well as in text.

The server is one more client of `/api/v1`. It can do nothing the API does not already allow the same user to do.

Clients reach it over OAuth, with Worklode as the authorization server. Every write goes to a project the user confirmed.

Why: people who do not use a terminal still file and shape work. One API keeps one set of permissions, and a confirmed project keeps work from landing where nobody looks.

