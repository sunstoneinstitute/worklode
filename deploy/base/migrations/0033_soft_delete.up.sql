-- Deleting tasks and documents (docs/specs/044-deleting-tasks-and-documents.md §2).
--
-- Delete is a tombstone, not a state: the events log is append-only and every
-- event, state_log row, edge and artifact referencing a task or document stays
-- valid. deleted_at IS NULL is the whole predicate for "live"; deleted_by and
-- delete_justification are payload the tombstone carries, never filters.

ALTER TABLE tasks
    ADD COLUMN deleted_at           timestamptz,
    ADD COLUMN deleted_by           text REFERENCES actors(id),
    ADD COLUMN delete_justification text;

ALTER TABLE docs
    ADD COLUMN deleted_at           timestamptz,
    ADD COLUMN deleted_by           text REFERENCES actors(id),
    ADD COLUMN delete_justification text;

-- A tombstoned document releases its slug and its corpus number. `lode doc
-- delete` exists for a wrong corpus number or a duplicate import (044 §0), and
-- both fixes mean re-creating the document: an unconditional unique index would
-- refuse that with a collision against a row the operator cannot see. The index
-- names are unchanged, so CreateDoc's ErrDocExists mapping still fires.
DROP INDEX docs_project_slug;
DROP INDEX docs_project_kind_number;

CREATE UNIQUE INDEX docs_project_slug
    ON docs (project_id, slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, number) WHERE number IS NOT NULL AND deleted_at IS NULL;
