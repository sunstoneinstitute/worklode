-- Task edges are stored under their ontology names (WL-REQ-1655): child_of
-- becomes childOf, follow_up_to followUpTo, duplicate_of duplicateOf. The
-- partial unique and lookup indexes carry the type in their predicate, so they
-- are rebuilt.
BEGIN;

DROP INDEX task_edges_single_parent;
DROP INDEX task_edges_children;
DROP INDEX task_edges_single_origin;
DROP INDEX task_edges_single_canonical;
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;

UPDATE task_edges SET type = CASE type
    WHEN 'child_of' THEN 'childOf'
    WHEN 'follow_up_to' THEN 'followUpTo'
    WHEN 'duplicate_of' THEN 'duplicateOf'
    ELSE type END;

ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('childOf','blocks','followUpTo','duplicateOf'));

CREATE UNIQUE INDEX task_edges_single_parent ON task_edges (from_task) WHERE type = 'childOf';
CREATE INDEX task_edges_children ON task_edges (to_task) WHERE type = 'childOf';
CREATE UNIQUE INDEX task_edges_single_origin ON task_edges (from_task) WHERE type = 'followUpTo';
CREATE UNIQUE INDEX task_edges_single_canonical ON task_edges (from_task) WHERE type = 'duplicateOf';

COMMIT;
