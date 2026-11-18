-- WL-REQ-1895: the event retention sweep checks that no row references an
-- event before deleting it, and each ON DELETE RESTRICT foreign key checks
-- again per deleted row. Without these indexes every check scans the table.
CREATE INDEX state_log_event_id ON state_log (event_id);
CREATE INDEX artifact_evidence_event_id ON artifact_evidence (event_id);
CREATE INDEX tasks_informed_by ON tasks (informed_by) WHERE informed_by IS NOT NULL;
