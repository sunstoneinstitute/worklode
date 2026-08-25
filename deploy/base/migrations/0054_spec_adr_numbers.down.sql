-- Reverse 0054: specs and ADRs give their counters back.
DELETE FROM project_entity_seq WHERE kind IN ('SPEC', 'ADR');

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN'));
