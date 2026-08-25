-- Reverse 0052: plans give their numbers back.
DELETE FROM project_entity_seq WHERE kind = 'PLAN';

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL'));

UPDATE docs SET number = NULL WHERE kind = 'plan';
ALTER TABLE docs ALTER COLUMN number DROP NOT NULL;

DROP INDEX docs_project_kind_number;
CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, number) WHERE number IS NOT NULL AND deleted_at IS NULL;

ALTER TABLE docs ADD CONSTRAINT docs_number_matches_kind
    CHECK ((kind = 'plan') = (number IS NULL));
