DELETE FROM project_entity_seq WHERE kind = 'MILE';

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR'));

DROP INDEX deliverables_milestone_idx;
DROP INDEX tasks_milestone_idx;
ALTER TABLE deliverables DROP COLUMN milestone_id;
ALTER TABLE tasks DROP COLUMN milestone_id;
DROP TABLE milestones;
