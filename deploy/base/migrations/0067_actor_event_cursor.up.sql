-- Per-actor Morning Brief boundary (029 §8.2, 032 §9). A read cursor over
-- the events log, like event_subscribers' offsets: bookkeeping, not a fact,
-- so writes to it are plain row writes, not events.
CREATE TABLE actor_event_cursor (
    actor_id      text PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    last_event_id bigint NOT NULL CHECK (last_event_id >= 0),
    updated_at    timestamptz NOT NULL
);
