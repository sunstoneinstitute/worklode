
| Harness | Export | Edge Agent reads |
|---|---|---|
| Claude Code | `claude_code.token.usage` metrics | token type, model, session id, timestamp, agent or subagent identity, resource attributes. `claude_code.cost.usage` is ignored; Worklode prices from `model_prices`. |
| Codex | OTel log records (the log exporter is required; its metrics carry no token counter) | token counts from completed-response events, normalised to the same usage classes |

