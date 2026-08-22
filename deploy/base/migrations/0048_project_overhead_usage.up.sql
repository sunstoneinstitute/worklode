-- One row per (project, agent, external session id, day, model, speed).
-- Mirrors agent_session_usage's shape, but keyed to a project directly:
-- overhead usage has no lease to hang off (a main-checkout session holds no
-- task's lease at report time), so there is no agent_sessions row for it.
--
-- Replaced wholesale per (project, agent, external session id), never
-- incremented -- same reason as agent_session_usage: the source transcript is
-- cumulative, so a report carries an absolute total that must overwrite a
-- prior one, not add to it.
CREATE TABLE project_overhead_usage (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    agent                 text NOT NULL,
    external_session_id   text NOT NULL,
    usage_day             date NOT NULL,
    model                 text NOT NULL,
    speed                 text NOT NULL DEFAULT 'standard',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    -- NULL means "no price on file for this model on this day" -- see
    -- agent_session_usage.cost_amount's identical comment.
    cost_amount           numeric(14,6),
    cost_currency         text NOT NULL DEFAULT 'USD',
    PRIMARY KEY (project_id, agent, external_session_id, usage_day, model, speed),
    CONSTRAINT project_overhead_usage_speed_known CHECK (speed IN ('standard', 'fast')),
    CONSTRAINT project_overhead_usage_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT project_overhead_usage_nonnegative CHECK (
        input_tokens >= 0 AND cache_write_5m_tokens >= 0 AND
        cache_write_1h_tokens >= 0 AND cache_read_tokens >= 0 AND
        output_tokens >= 0)
);

CREATE INDEX project_overhead_usage_day ON project_overhead_usage (usage_day);

-- Derived rollup, recomputed from scratch for the affected (project, day)
-- pairs whenever a (project, agent, session) overhead report is replaced.
-- Its own table rather than new columns on project_daily_cost: that table's
-- rows are, by construction, exactly what agent_session_usage sums up
-- through the lease -> task chain, and overhead has no task to join
-- through.
CREATE TABLE project_daily_overhead_cost (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    usage_day             date NOT NULL,
    cost_currency         text NOT NULL DEFAULT 'USD',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    cost_amount           numeric(14,6) NOT NULL DEFAULT 0,
    unpriced_tokens       bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, usage_day, cost_currency),
    CONSTRAINT project_daily_overhead_cost_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$')
);
