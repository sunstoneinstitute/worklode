-- Design clauses (docs/specs2/12-spec-refactoring-design-tree.md S8 to S11,
-- S20): every anchored section of a spec or ADR is a clause with a project
-- counter number (WL-CL-<n>, drawn from project_entity_seq kind 'CL'), a
-- status of its own and immutable versions. A document arranges clauses in
-- order; the arrangement is rebuilt on every body write.
CREATE TABLE clauses (
    id          bigserial PRIMARY KEY,
    project_id  text NOT NULL REFERENCES projects(id),
    number      bigint NOT NULL,
    status      text NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'accepted', 'superseded', 'withdrawn')),
    version     integer NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, number)
);

-- One row per version. A draft version is rewritten in place; an accepted
-- version's text never changes.
CREATE TABLE clause_versions (
    clause_id   bigint NOT NULL REFERENCES clauses(id) ON DELETE CASCADE,
    version     integer NOT NULL,
    heading     text NOT NULL,
    body        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (clause_id, version)
);

-- The document's current arrangement: which clause sits at which position and
-- heading depth, under which anchor. Rewritten with doc_sections.
CREATE TABLE doc_clauses (
    doc_id          bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    position        integer NOT NULL,
    clause_id       bigint NOT NULL REFERENCES clauses(id),
    clause_version  integer NOT NULL,
    depth           integer NOT NULL,
    anchor          text NOT NULL,
    PRIMARY KEY (doc_id, position),
    FOREIGN KEY (clause_id, clause_version) REFERENCES clause_versions(clause_id, version)
);
CREATE INDEX doc_clauses_clause ON doc_clauses (clause_id);

-- A task's governing clauses (S2, S3): direct links, materialised when a plan
-- mints the task from the plan's coverage, or added by hand. The clause
-- version current when the link was made is recorded; the link itself
-- resolves to the clause's newest version (S10).
CREATE TABLE task_governed_by (
    task_id         text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    clause_id       bigint NOT NULL REFERENCES clauses(id),
    clause_version  integer NOT NULL,
    source          text NOT NULL CHECK (source IN ('plan', 'manual')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, clause_id)
);
CREATE INDEX task_governed_by_clause ON task_governed_by (clause_id);

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR', 'MILE', 'CL'));
