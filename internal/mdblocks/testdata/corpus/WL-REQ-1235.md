
Activity records live in ClickHouse's `otel_logs`, under ClickStack's retention. The Activity query projects, from `LogAttributes`, only the attributes each event's row shows:

| Event | Attributes |
|---|---|
| `claude_code.user_prompt` | `command_name` |
| `claude_code.subagent_completed` | `agent_type`, `total_tool_uses`, `duration_ms`, `total_tokens` |
| `claude_code.skill_activated` | `skill.name` |
| `claude_code.compaction` | `trigger`, `pre_tokens`, `post_tokens` |
| `claude_code.tool_decision` | `tool_name`, `source` |
| `claude_code.permission_mode_changed` | `from_mode`, `to_mode` |
| `claude_code.api_retries_exhausted` | `status_code`, `total_attempts` |

Every row also carries `event.name`, `Timestamp` and `session.id`. `Body`, prompt and response text, tool content and `user.*` attributes are outside the projection, so none of them reaches Worklode's memory, logs or pages.
