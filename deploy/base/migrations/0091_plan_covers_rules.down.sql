-- Each covers edge points back at the section arranging its rule, or at the
-- rule's ref when nothing arranges it. Plan doc_rules rows are not restored:
-- the old code rewrites them on the plan's next write.
BEGIN;

DROP VIEW covered_sections;
DROP VIEW covered_rules;

DROP INDEX doc_edges_to_rule;
DROP INDEX doc_edges_unique;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_covers_rule;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_one_target;

UPDATE doc_edges e SET to_doc = a.doc_id, to_anchor = a.anchor, to_rule = NULL
  FROM (SELECT DISTINCT ON (rule_id) rule_id, doc_id, anchor
          FROM doc_rules ORDER BY rule_id, doc_id) a
 WHERE e.to_rule = a.rule_id;

UPDATE doc_edges e SET to_external = p.key || '-RULE-' || r.number, to_rule = NULL
  FROM rules r JOIN projects p ON p.id = r.project_id
 WHERE e.to_rule = r.id;

ALTER TABLE doc_edges DROP COLUMN to_rule;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_check CHECK ((to_doc IS NULL) <> (to_external IS NULL));
CREATE UNIQUE INDEX doc_edges_unique ON doc_edges
    (from_doc, coalesce(from_anchor, ''), type, coalesce(to_doc, 0),
     coalesce(to_anchor, ''), coalesce(to_external, ''));

COMMIT;
