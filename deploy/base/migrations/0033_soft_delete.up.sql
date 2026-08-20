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

-- Every list, ranking and pickup path adds "deleted_at IS NULL", so the live
-- set is what nearly every read wants. Partial indexes keep those reads on the
-- same plan they had before the column existed.
CREATE INDEX tasks_live ON tasks (project_id, state) WHERE deleted_at IS NULL;
CREATE INDEX docs_live  ON docs  (project_id, kind)  WHERE deleted_at IS NULL;
