-- WL-119: the graph projector's watermark becomes a commit horizon.
-- state_log ids come from a sequence assigned at INSERT time, not at commit
-- time, so two concurrent transactions in one process can commit out of id
-- order: the projector could scan past the higher id, checkpoint there, and
-- never see the lower one when its transaction finally committed. txid
-- records the writing transaction, and DirtyProjects now reads only
-- transactions below pg_snapshot_xmin — the rule the event log has carried
-- since 0021 (spec 025 §15) — so nothing can appear behind the watermark.
--
-- The volatile DEFAULT rewrites state_log under an ACCESS EXCLUSIVE lock,
-- safe for the same reason 0021's rewrite of events was, and with the same
-- expiry date: the table is small (thousands of rows), so the lock is held
-- for milliseconds while every ingest blocks on it. Existing rows take the
-- migration's own transaction id and drop below the horizon the moment it
-- commits.
ALTER TABLE state_log ADD COLUMN txid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE INDEX state_log_txid_id ON state_log (txid, id);

-- The watermark changes units with the scan: last_state_log_id held a
-- state_log id, last_txid holds the transaction id through which every
-- project has been rendered. The two sequences are unrelated, so the old
-- value cannot be carried over; starting from 0 re-projects every project
-- once, which is idempotent (whole graphs are replaced, deterministically
-- rendered).
ALTER TABLE graph_projection DROP COLUMN last_state_log_id;
ALTER TABLE graph_projection ADD COLUMN last_txid bigint NOT NULL DEFAULT 0;
