-- state_log's entity index was (entity_kind, id), which a lookup by entity_id
-- cannot use, so every per-entity read was a full scan. ListProjectWorkFacts
-- does one such read per task, making the main, project and progress pages
-- cost O(tasks x state_log rows): 2.3s over 838 tasks on the dev instance,
-- 8ms with this index. The trailing columns serve the ORDER BY ... LIMIT 1
-- that read wraps around it.
CREATE INDEX state_log_entity ON state_log (entity_kind, entity_id, at DESC, id DESC);
