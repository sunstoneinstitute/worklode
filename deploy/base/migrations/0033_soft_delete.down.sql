-- Restore migration 0027's unconditional unique indexes before the column they
-- are now partial on goes away.
DROP INDEX docs_project_kind_number;
DROP INDEX docs_project_slug;

CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, number) WHERE number IS NOT NULL;
CREATE UNIQUE INDEX docs_project_slug ON docs (project_id, slug);

ALTER TABLE docs
    DROP COLUMN delete_justification,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at;

ALTER TABLE tasks
    DROP COLUMN delete_justification,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at;
