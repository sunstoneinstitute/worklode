-- Narrowing a CHECK fails on any row outside it, so the edges go first. They
-- are provenance and nothing derives from them, so dropping them loses a
-- record and breaks nothing.
DROP INDEX IF EXISTS task_edges_single_canonical;
DELETE FROM task_edges WHERE type = 'duplicate_of';
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;
ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('child_of','blocks','follow_up_to'));
