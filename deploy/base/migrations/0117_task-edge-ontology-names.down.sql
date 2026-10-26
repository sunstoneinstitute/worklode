BEGIN;

DROP INDEX task_edges_single_parent;
DROP INDEX task_edges_children;
DROP INDEX task_edges_single_origin;
DROP INDEX task_edges_single_canonical;
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;

UPDATE task_edges SET type = CASE type
    WHEN 'childOf' THEN 'child_of'
    WHEN 'followUpTo' THEN 'follow_up_to'
    WHEN 'duplicateOf' THEN 'duplicate_of'
    ELSE type END;

ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('child_of','blocks','follow_up_to','duplicate_of'));

CREATE UNIQUE INDEX task_edges_single_parent ON task_edges (from_task) WHERE type = 'child_of';
CREATE INDEX task_edges_children ON task_edges (to_task) WHERE type = 'child_of';
CREATE UNIQUE INDEX task_edges_single_origin ON task_edges (from_task) WHERE type = 'follow_up_to';
CREATE UNIQUE INDEX task_edges_single_canonical ON task_edges (from_task) WHERE type = 'duplicate_of';

COMMIT;
