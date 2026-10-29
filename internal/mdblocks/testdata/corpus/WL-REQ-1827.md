
`project_use(project, confirmed?, from_instructions?)` confirms which project a conversation works in and returns a *project handle*: the project key plus a signature over the calling user, the key and an expiry 24 hours ahead, for example `WL.k3f9q2`. It stores nothing. `project` must name a project exactly: its key, id or full name, ignoring case.

- `from_instructions=true`: the user's Claude project instructions contain `Worklode project: <name>`. Claude calls this at the start of a conversation without asking. Anything but an exact match answers `confirm_project`.
- `confirmed=true`: only after the user explicitly confirmed this exact project.
- Neither: when the client speaks MCP protocol `2026-07-28` and supports form elicitation, the tool returns an input-required result asking the user directly ("Use the Worklode project Worklode (WL) for this conversation?"), and an accept counts as confirmed. Otherwise, or on a near-miss name, it answers `confirm_project` and returns no handle.

Why a signed handle: the server keeps no per-conversation state, and one connector token serves every chat on that connection. The handle carries the confirmation on each call instead, so two chats can work in two projects and neither changes the other.
