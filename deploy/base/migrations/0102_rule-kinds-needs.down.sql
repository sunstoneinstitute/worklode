-- Fails while any rule uses a new kind or any edge is a needs edge.
-- Reclassify or delete those rows first.
BEGIN;

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;
ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references', 'amends', 'supersedes', 'wasDerivedFrom'));

ALTER TABLE rules DROP CONSTRAINT rules_kind_check;
ALTER TABLE rules ADD CONSTRAINT rules_kind_check
    CHECK (kind IN ('requirement', 'invariant', 'informative'));

COMMIT;
