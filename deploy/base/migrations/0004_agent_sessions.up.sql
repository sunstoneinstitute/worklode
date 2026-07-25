-- One row per coding-agent session working a leased task. See
-- docs/specs/2026-07-25-agent-sessions-design.md.

CREATE TABLE agent_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lease_id            bigint NOT NULL REFERENCES leases(id) ON DELETE RESTRICT,
    agent               text NOT NULL,
    agent_version       text,
    external_session_id text NOT NULL,
    started_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    ended_at            timestamptz,
    input_tokens        bigint,
    output_tokens       bigint,
    cost_amount         numeric(12,6),
    cost_currency       text NOT NULL DEFAULT 'USD',
    CONSTRAINT agent_sessions_agent_known CHECK (agent IN
        ('claude-code','codex','cursor','aider',
         'opencode','pi','amp','other')),
    CONSTRAINT agent_sessions_cost_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT agent_sessions_lease_session_unique UNIQUE (lease_id, agent, external_session_id)
);
