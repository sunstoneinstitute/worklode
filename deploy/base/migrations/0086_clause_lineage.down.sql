BEGIN;

-- Discards any lineage edges written under this migration before the
-- increment 2 CHECKs are restored; a downgrade cannot keep rows a narrower
-- CHECK would reject.
DELETE FROM clause_edges WHERE type IN ('supersededBy', 'wasDerivedFrom') OR source = 'refactor';

ALTER TABLE clause_edges DROP CONSTRAINT clause_edges_source_check;
ALTER TABLE clause_edges ADD CONSTRAINT clause_edges_source_check
    CHECK (source IN ('manual', 'derived'));

ALTER TABLE clause_edges DROP CONSTRAINT clause_edges_type_check;
ALTER TABLE clause_edges ADD CONSTRAINT clause_edges_type_check
    CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references'));

COMMIT;
