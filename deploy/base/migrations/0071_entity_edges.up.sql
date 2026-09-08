-- References across projects (029 §5). Containment never crosses a project
-- boundary; these references do, which is why the ends are (kind, id) pairs
-- with no project column and no FK — each side's per-kind table carries
-- identity, and there is deliberately no unified entities table.
-- task_edges is NOT folded in: its rels are task-only, with real FKs into
-- tasks and the hierarchy/blocking queries tuned to them.
CREATE TABLE entity_edges (
    from_kind  text NOT NULL CHECK (from_kind IN ('project','milestone')),
    from_id    text NOT NULL,
    to_kind    text NOT NULL CHECK (to_kind IN ('deliverable','task')),
    to_id      text NOT NULL,
    rel        text NOT NULL CHECK (rel IN ('depends_on','seeded_by')),
    created_at timestamptz NOT NULL,
    created_by text REFERENCES actors (id) ON DELETE RESTRICT,
    PRIMARY KEY (from_kind, from_id, to_kind, to_id, rel)
);

-- The read is always "references touching this entity", from either end.
CREATE INDEX entity_edges_to_idx ON entity_edges (to_kind, to_id);
