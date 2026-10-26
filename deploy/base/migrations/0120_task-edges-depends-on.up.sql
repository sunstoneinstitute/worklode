-- Task ordering is stored as dependsOn (WL-REQ-1655): a row "A blocks B"
-- becomes "B dependsOn A". Only from_task, to_task and type change; every
-- other column, classification_id included, carries over.
BEGIN;

ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;

UPDATE task_edges SET from_task = to_task, to_task = from_task, type = 'dependsOn'
 WHERE type = 'blocks';

ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('childOf','dependsOn','followUpTo','duplicateOf'));

COMMIT;
