CREATE TABLE agent_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lease_id            bigint NOT NULL REFERENCES leases(id) ON DELETE RESTRICT,
    agent               text NOT NULL CHECK (agent IN
                          ('claude-code','codex','cursor','aider',
                           'opencode','pi','amp','other')),
    agent_version       text,
    external_session_id text NOT NULL,
    started_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    ended_at            timestamptz,
    input_tokens        bigint,
    output_tokens       bigint,
    cost_amount         numeric(12,6),
    cost_currency       text NOT NULL DEFAULT 'USD'
                          CHECK (cost_currency ~ '^[A-Z]{3}$'),
    UNIQUE (lease_id, agent, external_session_id)
);
CREATE INDEX agent_sessions_lease ON agent_sessions (lease_id);
