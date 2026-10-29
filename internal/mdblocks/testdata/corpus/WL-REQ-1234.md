
The task page shows how an agent session's work went, at the level of turns and exceptions, from these Claude Code OpenTelemetry log events:

- `claude_code.user_prompt`: a turn started.
- `claude_code.subagent_completed`: a subagent finished.
- `claude_code.skill_activated`: the agent took up a skill.
- `claude_code.compaction`: the context was compacted.
- `claude_code.tool_decision` with `decision=reject`: a tool call was blocked.
- `claude_code.permission_mode_changed`: the permission mode switched.
- `claude_code.api_retries_exhausted`: the agent stalled on the model API.

Token accounting uses a separate path (WL-REQ-279).

Claude Code's OTel configuration is distributed through MDM as managed settings. Its telemetry reaches the organisation's otel-gateway and lands in ClickStack's ClickHouse: log records in `otel_logs`, spans in `otel_traces`, and metrics in the `otel_metrics_*` tables.

Worklode reads task activity from the log records in `otel_logs` on request. A task's records are those attributed to it by WL-REQ-1730, filtered to the events above, projecting only the attributes WL-REQ-1235 allows. The server connects with a read-only ClickHouse user from `LODE_CLICKHOUSE_DSN`; with it unset the Activity card is omitted. A ClickHouse failure degrades the Activity card to an error line and leaves the rest of the task page intact.
