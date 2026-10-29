
```sql
CREATE TABLE agent_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lease_id            bigint NOT NULL REFERENCES leases(id) ON DELETE RESTRICT,
    agent               text NOT NULL
                          CONSTRAINT agent_sessions_agent_known CHECK (agent IN
                          ('claude-code','codex','copilot','cursor','aider','opencode','pi','amp','other')),
    agent_version       text,
    external_session_id text NOT NULL,
    started_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    ended_at            timestamptz,
    input_tokens        bigint,
    output_tokens       bigint,
    cost_amount         numeric(12,6),
    cost_currency       text NOT NULL DEFAULT 'USD'
                          CONSTRAINT agent_sessions_cost_currency_format
                          CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT agent_sessions_lease_session_unique
      UNIQUE (lease_id, agent, external_session_id)
);
```

- A child table of `leases`, because one lease outlives many sessions (restarts, `/clear`, next-day resumption).
- The unique key is per lease. A lease that expires mid-session and is re-claimed by the same live session produces a second row. `/clear` yields a new session id under the same lease. Concurrent open sessions on one lease are permitted.
- `external_session_id` is the tool's own id, namespaced by `agent`. `agent_version` is plumbed everywhere and empty for Claude Code, whose payload carries none.
- `input_tokens` and `output_tokens` are the headline rollup. Billable detail lives in `agent_session_usage`, keyed by (session, day, model, speed), priced from `model_prices`. `project_overhead_usage` and `project_daily_overhead_cost` hold usage no lease can bill.
- Cost is an amount plus an ISO 4217 code; `cost_amount IS NULL` means no cost recorded. `EndAgentSession` applies the `USD` default explicitly because a column DEFAULT fires only on INSERT.

