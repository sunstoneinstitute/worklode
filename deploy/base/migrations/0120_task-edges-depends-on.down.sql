BEGIN;

ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;

UPDATE task_edges SET from_task = to_task, to_task = from_task, type = 'blocks'
 WHERE type = 'dependsOn';

ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('childOf','blocks','followUpTo','duplicateOf'));

COMMIT;
