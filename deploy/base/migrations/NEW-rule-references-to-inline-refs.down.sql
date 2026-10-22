-- Rule-to-rule inline_refs rows move back into rule_edges (the current
-- version's) and rule_edge_versions (older versions'). Rows to other node
-- kinds have no rule_edges shape and stay.
BEGIN;

ALTER TABLE rule_edges DROP CONSTRAINT rule_edges_type_check;
ALTER TABLE rule_edges ADD CONSTRAINT rule_edges_type_check
    CHECK (type IN ('refines', 'needs', 'constrains', 'conflictsWith', 'references', 'amends', 'supersedes', 'wasDerivedFrom'));

INSERT INTO rule_edges (from_rule, to_rule, type, source, created_at)
SELECT r.id, t.id, 'references', max(i.source), min(i.created_at)
  FROM inline_refs i
  JOIN rules r ON r.id::text = i.from_id AND r.version = i.from_version
  JOIN rules t ON t.id::text = i.to_id
 WHERE i.from_kind = 'rule' AND i.to_kind = 'rule' AND r.id <> t.id
 GROUP BY r.id, t.id;

INSERT INTO rule_edge_versions (rule_id, version, to_rule, type, source)
SELECT r.id, i.from_version, t.id, 'references', max(i.source)
  FROM inline_refs i
  JOIN rules r ON r.id::text = i.from_id AND r.version > i.from_version
  JOIN rules t ON t.id::text = i.to_id
 WHERE i.from_kind = 'rule' AND i.to_kind = 'rule'
 GROUP BY r.id, i.from_version, t.id;

DELETE FROM inline_refs WHERE from_kind = 'rule' AND to_kind = 'rule';

COMMIT;
