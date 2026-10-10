-- Rule kinds catalogue, definition and principle, and the needs edge
-- (WL-REQ-165, WL-REQ-1367, WL-REQ-1288). informative stays valid until its rows are
-- reclassified (WL-REQ-1301).
BEGIN;

ALTER TABLE rules DROP CONSTRAINT rules_kind_check;
ALTER TABLE rules ADD CONSTRAINT rules_kind_check
    CHECK (kind IN ('requirement', 'catalogue', 'invariant', 'definition', 'principle', 'informative'));

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;
ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'needs', 'constrains', 'conflictsWith', 'references', 'amends', 'supersedes', 'wasDerivedFrom'));

COMMIT;
