-- Reverse the schema half of retiring the adr kind: both CHECKs accept ADRs
-- again and former_adr goes. The data half is one-way and stays: former ADRs
-- remain specs at their new numbers (each body's first line still names the
-- old ref), and the hard-deleted tombstones are not restored.

ALTER TABLE docs DROP CONSTRAINT docs_kind_check;
ALTER TABLE docs ADD CONSTRAINT docs_kind_check CHECK (kind IN ('spec', 'adr', 'plan'));

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR', 'MILE', 'RULE'));

ALTER TABLE docs DROP COLUMN former_adr;
