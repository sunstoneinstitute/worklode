-- Back to the state_log-id watermark, restarted at 0 for the same
-- units-changed reason the up migration restarted last_txid: a full
-- re-projection, not a skipped one.
ALTER TABLE graph_projection DROP COLUMN last_txid;
ALTER TABLE graph_projection ADD COLUMN last_state_log_id bigint NOT NULL DEFAULT 0;

DROP INDEX state_log_txid_id;
ALTER TABLE state_log DROP COLUMN txid;
