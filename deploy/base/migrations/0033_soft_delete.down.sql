DROP INDEX docs_live;
DROP INDEX tasks_live;

ALTER TABLE docs
    DROP COLUMN delete_justification,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at;

ALTER TABLE tasks
    DROP COLUMN delete_justification,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at;
