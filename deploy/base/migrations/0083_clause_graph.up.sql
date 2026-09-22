-- Clause graph (docs/specs2/12-spec-refactoring-design-tree.md S10, S12,
-- S15, S26): typed edges between clauses, a pinned version on a governing
-- link, and an owner and tags on a clause.

-- One row per edge. type is the wl: property; source says whether an
-- architect wrote it (manual) or the store derived it from the clause text
-- (derived, only ever type 'references'). The key includes type so two
-- clauses may relate in more than one way.
CREATE TABLE clause_edges (
    from_clause bigint NOT NULL REFERENCES clauses(id) ON DELETE CASCADE,
    to_clause   bigint NOT NULL REFERENCES clauses(id) ON DELETE CASCADE,
    type        text NOT NULL CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references')),
    source      text NOT NULL CHECK (source IN ('manual', 'derived')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (from_clause, to_clause, type),
    CHECK (from_clause <> to_clause)
);
CREATE INDEX clause_edges_to ON clause_edges (to_clause);

ALTER TABLE clauses ADD COLUMN owner text;
ALTER TABLE clauses ADD COLUMN tags text[] NOT NULL DEFAULT '{}';

-- A pinned link resolves to this version instead of the clause's newest (S10).
ALTER TABLE task_governed_by ADD COLUMN pinned_version integer;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_pinned_fkey
    FOREIGN KEY (clause_id, pinned_version) REFERENCES clause_versions(clause_id, version);
