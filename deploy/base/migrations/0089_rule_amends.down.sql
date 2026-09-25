BEGIN;

-- amends rows cannot survive the narrower CHECK; supersedes rows swap back.
DELETE FROM rule_edges WHERE type = 'amends';

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;

UPDATE rule_edges SET from_rule = to_rule, to_rule = from_rule, type = 'supersededBy'
 WHERE type = 'supersedes';

ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'constrains', 'conflictsWith', 'references', 'supersededBy', 'wasDerivedFrom'));

COMMIT;
