-- Deliverables (docs/specs/029-research-work-in-the-backbone.md §3): a
-- declared, checkable output of a project — a datapackage, a report PDF, a CMS
-- post. A deliverable is its own entity, never a task kind (§2): it cannot be
-- claimed, worked, or closed, so modelling it as a task would store a row that
-- lies about what it is.
--
-- It carries no state column, by design (§3.2). "Is it live" is answered by
-- facts reported by emitters and probers, never by a human closing a task;
-- until those ingest paths exist the cockpit says the state is unreported
-- rather than storing a claim nobody verified. The three descriptive fields
-- here are exactly the ones §3.1 allows a person to supply for a custom
-- deliverable: name, description, and an optional URL.
--
-- Milestones (§2) are not built yet, so a deliverable hangs off its project
-- directly; the nullable milestone_id arrives with the milestone table.

-- Per-project ordinal counters for the non-task entity kinds (§4). Tasks keep
-- their own projects.next_task_num counter; every other kind draws from a
-- (project_id, kind) row here, which is what makes a deliverable COW-DEL-3.
-- The CHECK carries only the kinds that exist — widen it when the entity
-- arrives, exactly as the tasks.kind CHECK is widened.
CREATE TABLE project_entity_seq (
    project_id text   NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    kind       text   NOT NULL CHECK (kind IN ('DEL')),
    next       bigint NOT NULL,
    PRIMARY KEY (project_id, kind)
);

CREATE TABLE deliverables (
    id          text PRIMARY KEY,                                   -- <KEY>-DEL-<n>
    project_id  text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    url         text NOT NULL DEFAULT '',
    created_by  text REFERENCES actors (id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);

CREATE INDEX deliverables_project_idx ON deliverables (project_id, created_at, id);

-- The cockpit's web forms are their own ingest source. A person creating work
-- through a browser session is neither the token-authenticated CLI/API ('cli')
-- nor the server acting on its own ('system'), and the event log is the
-- provenance record (004) — collapsing the two would make "who typed this"
-- unanswerable.
ALTER TABLE events DROP CONSTRAINT events_source_check;
ALTER TABLE events ADD CONSTRAINT events_source_check
    CHECK (source IN ('github','flux','watcher','cli','system','web'));
