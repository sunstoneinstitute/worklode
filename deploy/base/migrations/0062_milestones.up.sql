-- Milestones (spec 029 §2): project → milestone → {task, deliverable}.
-- Progress is derived from contained work, never stored on the milestone.
CREATE TABLE milestones (
    id          text PRIMARY KEY,
    project_id  text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    title       text NOT NULL,
    position    integer NOT NULL,
    created_by  text REFERENCES actors (id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);

CREATE INDEX milestones_project_idx ON milestones (project_id, position, id);

ALTER TABLE tasks        ADD COLUMN milestone_id text REFERENCES milestones (id) ON DELETE SET NULL;
ALTER TABLE deliverables ADD COLUMN milestone_id text REFERENCES milestones (id) ON DELETE SET NULL;

CREATE INDEX tasks_milestone_idx        ON tasks (milestone_id)        WHERE milestone_id IS NOT NULL;
CREATE INDEX deliverables_milestone_idx ON deliverables (milestone_id) WHERE milestone_id IS NOT NULL;

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR', 'MILE'));
