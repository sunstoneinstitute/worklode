-- Clause lineage edges (docs/specs2/12-spec-refactoring-design-tree.md S22):
-- widen clause_edges to carry the two lineage edge types a refactor writes.
-- type gains supersededBy (a refactor's merge/withdraw result, one writer:
-- lode clause supersede) and wasDerivedFrom (an ordinary manual edge, e.g.
-- from a split). source gains refactor, the provenance of a supersededBy
-- edge, which cannot be removed by lode clause unlink.
BEGIN;

ALTER TABLE clause_edges DROP CONSTRAINT clause_edges_type_check;
ALTER TABLE clause_edges ADD CONSTRAINT clause_edges_type_check
    CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references', 'supersededBy', 'wasDerivedFrom'));

ALTER TABLE clause_edges DROP CONSTRAINT clause_edges_source_check;
ALTER TABLE clause_edges ADD CONSTRAINT clause_edges_source_check
    CHECK (source IN ('manual', 'derived', 'refactor'));

COMMIT;
