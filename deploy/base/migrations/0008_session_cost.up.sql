-- Token accounting and cost for agent sessions.
--
-- A vendor turn's prompt is not one number. It splits into four separately
-- priced input classes -- uncached input, a cache write at the 5-minute TTL,
-- a cache write at the 1-hour TTL, and a cache read -- plus output. The
-- rates differ by up to 20x between classes, so agent_sessions' original
-- input_tokens/output_tokens pair cannot express a bill. Those two columns
-- stay as the session-level roll-up of the two headline classes; the
-- per-class detail lives here.
--
-- Output tokens are never cached. Last turn's output becomes this turn's
-- input and is cached there, so its long tail is billed as cache_read, not
-- as output.

-- Rates per model, in micro-units of `currency` per million tokens. Integers,
-- so cost arithmetic never touches a float: an amount is
-- tokens * micros / 1e6, evaluated in integer micro-currency.
--
-- Effective-dated because vendor rates change and a past session must keep
-- pricing at the rate that applied when it ran. A lookup takes the newest row
-- with effective_from <= the usage day.
--
-- Every class gets an explicit rate rather than a multiplier off input: the
-- 1.25x / 2x / 0.1x cache multipliers are vendor-wide convention today, not a
-- guarantee, and an operator correcting one class should not have to reason
-- about which constant is hidden in the code.
CREATE TABLE model_prices (
    model                 text NOT NULL,
    -- Fast mode is a different SKU, not a modifier: it is priced separately
    -- rather than derived, because nothing guarantees it stays a clean
    -- multiple of standard.
    speed                 text NOT NULL DEFAULT 'standard',
    effective_from        date NOT NULL,
    currency              text NOT NULL DEFAULT 'USD',
    input_micros          bigint NOT NULL,
    cache_write_5m_micros bigint NOT NULL,
    cache_write_1h_micros bigint NOT NULL,
    cache_read_micros     bigint NOT NULL,
    output_micros         bigint NOT NULL,
    PRIMARY KEY (model, speed, effective_from),
    CONSTRAINT model_prices_speed_known CHECK (speed IN ('standard', 'fast')),
    CONSTRAINT model_prices_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT model_prices_nonnegative CHECK (
        input_micros >= 0 AND cache_write_5m_micros >= 0 AND
        cache_write_1h_micros >= 0 AND cache_read_micros >= 0 AND
        output_micros >= 0)
);

-- One row per (session, day, model, speed). Day, because a session that runs
-- past midnight must split its cost across both days for the per-day rollup;
-- model, because one coding session routinely mixes models -- a main loop on
-- one, subagents on another -- at rates that differ several-fold.
--
-- Rows are replaced wholesale per session, never incremented: the source
-- transcript is cumulative, so a report carries absolute totals and a second
-- report of the same session must overwrite rather than add.
CREATE TABLE agent_session_usage (
    agent_session_id      bigint NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    usage_day             date NOT NULL,
    model                 text NOT NULL,
    speed                 text NOT NULL DEFAULT 'standard',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    -- NULL means "no price on file for this model on this day", which is
    -- deliberately distinct from a zero cost: an unknown model must not be
    -- silently billed at nothing.
    cost_amount           numeric(14,6),
    cost_currency         text NOT NULL DEFAULT 'USD',
    PRIMARY KEY (agent_session_id, usage_day, model, speed),
    CONSTRAINT agent_session_usage_speed_known CHECK (speed IN ('standard', 'fast')),
    CONSTRAINT agent_session_usage_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$'),
    CONSTRAINT agent_session_usage_nonnegative CHECK (
        input_tokens >= 0 AND cache_write_5m_tokens >= 0 AND
        cache_write_1h_tokens >= 0 AND cache_read_tokens >= 0 AND
        output_tokens >= 0)
);

-- Supports the per-project rollup recompute, which filters by day across
-- every session.
CREATE INDEX agent_session_usage_day ON agent_session_usage (usage_day);

-- Derived rollup: agent_session_usage aggregated up the
-- session -> lease -> task -> project chain. Recomputed from scratch for the
-- affected (project, day) pairs whenever a session's usage is replaced, so it
-- can be rebuilt at any time and cannot drift.
--
-- Currency is part of the key, not an attribute: amounts in different
-- currencies must never be summed into one number, and converting them needs
-- a dated rate source this table has no business owning.
CREATE TABLE project_daily_cost (
    project_id            text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    usage_day             date NOT NULL,
    cost_currency         text NOT NULL DEFAULT 'USD',
    input_tokens          bigint NOT NULL DEFAULT 0,
    cache_write_5m_tokens bigint NOT NULL DEFAULT 0,
    cache_write_1h_tokens bigint NOT NULL DEFAULT 0,
    cache_read_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    cost_amount           numeric(14,6) NOT NULL DEFAULT 0,
    -- Tokens that reached this project on a day with no price on file. The
    -- honest alternative to folding them in at zero: a reader can see that
    -- the amount understates the bill, and by how much.
    unpriced_tokens       bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, usage_day, cost_currency),
    CONSTRAINT project_daily_cost_currency_format CHECK (cost_currency ~ '^[A-Z]{3}$')
);

-- Seed rates. effective_from 2000-01-01 is "since before any session this
-- backbone recorded", so historical usage prices rather than reading as
-- unpriced. Cache-write rates are 1.25x (5m TTL) and 2x (1h TTL) base input;
-- cache reads are 0.1x.
INSERT INTO model_prices
    (model, speed, effective_from, currency,
     input_micros, cache_write_5m_micros, cache_write_1h_micros,
     cache_read_micros, output_micros)
VALUES
    -- Opus tier: $5 / $25 per MTok.
    ('claude-opus-5',     'standard', DATE '2000-01-01', 'USD',  5000000,  6250000, 10000000,  500000, 25000000),
    ('claude-opus-4-8',   'standard', DATE '2000-01-01', 'USD',  5000000,  6250000, 10000000,  500000, 25000000),
    ('claude-opus-4-7',   'standard', DATE '2000-01-01', 'USD',  5000000,  6250000, 10000000,  500000, 25000000),
    ('claude-opus-4-6',   'standard', DATE '2000-01-01', 'USD',  5000000,  6250000, 10000000,  500000, 25000000),
    ('claude-opus-4-5',   'standard', DATE '2000-01-01', 'USD',  5000000,  6250000, 10000000,  500000, 25000000),
    ('claude-opus-4-1',   'standard', DATE '2000-01-01', 'USD', 15000000, 18750000, 30000000, 1500000, 75000000),
    -- Fast mode on the Opus tier: $10 / $50 per MTok.
    ('claude-opus-5',     'fast',     DATE '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
    ('claude-opus-4-8',   'fast',     DATE '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
    -- Fable/Mythos tier: $10 / $50 per MTok.
    ('claude-fable-5',    'standard', DATE '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
    ('claude-mythos-5',   'standard', DATE '2000-01-01', 'USD', 10000000, 12500000, 20000000, 1000000, 50000000),
    -- Sonnet 5 launched on introductory pricing of $2 / $10, reverting to
    -- $3 / $15 on 2026-09-01. Two dated rows, which is the whole point of
    -- effective_from: a July session must not be repriced by a September rate.
    ('claude-sonnet-5',   'standard', DATE '2000-01-01', 'USD',  2000000,  2500000,  4000000,  200000, 10000000),
    ('claude-sonnet-5',   'standard', DATE '2026-09-01', 'USD',  3000000,  3750000,  6000000,  300000, 15000000),
    ('claude-sonnet-4-6', 'standard', DATE '2000-01-01', 'USD',  3000000,  3750000,  6000000,  300000, 15000000),
    ('claude-sonnet-4-5', 'standard', DATE '2000-01-01', 'USD',  3000000,  3750000,  6000000,  300000, 15000000),
    -- Haiku tier: $1 / $5 per MTok.
    ('claude-haiku-4-5',  'standard', DATE '2000-01-01', 'USD',  1000000,  1250000,  2000000,  100000,  5000000);
