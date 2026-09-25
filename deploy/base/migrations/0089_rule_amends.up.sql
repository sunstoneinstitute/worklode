-- Rule edges gain amends, and supersession is stored new -> old (WL-SPEC-77
-- §4): supersededBy rows swap their ends and become supersedes. The inverses
-- (amendedBy, supersededBy) are read from the far end and never stored.
BEGIN;

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;

UPDATE rule_edges SET from_rule = to_rule, to_rule = from_rule, type = 'supersedes'
 WHERE type = 'supersededBy';

ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references', 'amends', 'supersedes', 'wasDerivedFrom'));

COMMIT;
