-- Rule references move from rule_edges into inline_refs, and rule_edges stops
-- storing them (WL-REQ-1852, WL-REQ-1640). A live edge lands on the rule's
-- current version, a snapshot on the version it was taken at; both on the
-- body field with no line numbers, which the next derive fills in.
BEGIN;

INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source, created_at)
SELECT 'rule', e.from_rule::text, 'body', r.version, 'rule', e.to_rule::text, '{}',
       CASE e.source WHEN 'derived' THEN 'derived' ELSE 'manual' END, e.created_at
  FROM rule_edges e JOIN rules r ON r.id = e.from_rule
 WHERE e.type = 'references';

INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source)
SELECT 'rule', v.rule_id::text, 'body', v.version, 'rule', v.to_rule::text, '{}',
       CASE v.source WHEN 'derived' THEN 'derived' ELSE 'manual' END
  FROM rule_edge_versions v
 WHERE v.type = 'references';

DELETE FROM rule_edges WHERE type = 'references';
DELETE FROM rule_edge_versions WHERE type = 'references';

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;
ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'needs', 'constrains', 'conflictsWith', 'amends', 'supersedes', 'wasDerivedFrom'));

COMMIT;
