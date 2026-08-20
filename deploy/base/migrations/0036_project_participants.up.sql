-- Project Crew (spec 029 §6.1): role-labelled participant rows, visible
-- before any task is picked up. One actor may hold several role labels
-- (one row each); at most one row per project carries is_lead.
CREATE TABLE project_participants (
    project_id text        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    actor_id   text        NOT NULL REFERENCES actors (id)   ON DELETE RESTRICT,
    role       text        NOT NULL,
    is_lead    boolean     NOT NULL DEFAULT false,
    added_at   timestamptz NOT NULL,
    added_by   text        REFERENCES actors (id) ON DELETE RESTRICT,
    PRIMARY KEY (project_id, actor_id, role)
);

CREATE UNIQUE INDEX project_participants_one_lead
    ON project_participants (project_id) WHERE is_lead;
