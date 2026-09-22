DELETE FROM project_entity_seq WHERE kind = 'CL';

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR', 'MILE'));

DROP TABLE task_governed_by;
DROP TABLE doc_clauses;
DROP TABLE clause_versions;
DROP TABLE clauses;
