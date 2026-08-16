DROP TABLE event_subscribers;
DROP INDEX events_txid_id;
ALTER TABLE events DROP COLUMN txid;
