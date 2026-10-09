-- Rule kinds catalogue, definition and principle, and the needs edge
-- (WL-SPEC-77 §4, §4c, §8.1). informative stays valid until its rows are
-- reclassified (§19.7).
BEGIN;

ALTER TABLE rules DROP CONSTRAINT rules_kind_check;
ALTER TABLE rules ADD CONSTRAINT rules_kind_check
    CHECK (kind IN ('requirement', 'catalogue', 'invariant', 'definition', 'principle', 'informative'));

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;
ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'needs', 'constrains', 'conflictsWith', 'references', 'amends', 'supersedes', 'wasDerivedFrom'));

COMMIT;
