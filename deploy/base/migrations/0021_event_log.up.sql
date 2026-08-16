-- Spec 025 §15: total order for the event log. txid records the writing
-- transaction; readers take only rows below the commit horizon
-- (pg_snapshot_xmin), so a transaction that commits late can never surface
-- an id behind a subscriber's offset. xid8 is 64-bit: no wraparound
-- handling in the comparison.
--
-- The volatile DEFAULT forces a full rewrite of events under an ACCESS
-- EXCLUSIVE lock. The one thing that makes this safe is that the log is
-- small (thousands of rows), so the lock is held for milliseconds. It is
-- NOT safe because nothing is running: deploy/base/deployment.yaml is
-- replicas: 1 with the default RollingUpdate strategy and a migrate
-- initContainer,
-- so this rewrite runs while the old pod is still serving, and for its
-- duration every RecordEvent — every webhook, every state change — blocks
-- on the lock. Every pre-existing row takes the migration's own
-- transaction id and drops below the horizon the moment the migration
-- commits. Once the table is large enough that the rewrite is no longer
-- measured in milliseconds, the two-step ADD NULL + backfill dance is the
-- escape hatch — do not need it now, do not build it now.
ALTER TABLE events ADD COLUMN txid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE INDEX events_txid_id ON events (txid, id);

-- Spec 025 §15.1: the durable half of a consumer group, one row per
-- subscriber. Offsets are event ids: monotonic positions, not counts —
-- aborted transactions leave holes and nothing depends on contiguity.
CREATE TABLE event_subscribers (
    name              text PRIMARY KEY,
    last_read_offset  bigint NOT NULL DEFAULT 0,
    last_acked_offset bigint NOT NULL DEFAULT 0,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT event_subscribers_acked_le_read
        CHECK (last_acked_offset <= last_read_offset)
);
