-- A covers entry naming a section also covers every rule arranged under it
-- (WL-SPEC-77 §4, WL-SPEC-78 §4.1). 0091 resolved a section entry to the rule
-- at its anchor only, so each covers edge now extends to its rule's subtree:
-- the later rules of the arranging document up to the next heading at the
-- same depth or shallower. A rule the plan already covers keeps its edge, and
-- a rule under two covered ancestors takes the level of the nearer one. No
-- plan carried a rule-ref entry when this ran, so every edge is a section or
-- whole-document one.
BEGIN;

CREATE TEMP TABLE covers_subtree ON COMMIT DROP AS
SELECT DISTINCT ON (e.from_doc, coalesce(e.from_anchor, ''), s.rule_id)
       e.id AS src_id, e.from_doc, e.from_anchor, s.rule_id, e.coverage, e.declared_by
  FROM doc_edges e
  JOIN doc_rules a ON a.rule_id = e.to_rule
  JOIN doc_rules s ON s.doc_id = a.doc_id AND s.position > a.position
 WHERE e.type = 'covers'
   AND NOT EXISTS (SELECT 1 FROM doc_rules n
                    WHERE n.doc_id = a.doc_id AND n.depth <= a.depth
                      AND n.position > a.position AND n.position <= s.position)
   AND NOT EXISTS (SELECT 1 FROM doc_edges c
                    WHERE c.type = 'covers' AND c.from_doc = e.from_doc
                      AND c.from_anchor IS NOT DISTINCT FROM e.from_anchor
                      AND c.to_rule = s.rule_id)
 ORDER BY e.from_doc, coalesce(e.from_anchor, ''), s.rule_id, a.depth DESC, e.id;

INSERT INTO doc_edges (from_doc, from_anchor, type, to_rule, coverage, declared_by)
SELECT from_doc, from_anchor, 'covers', rule_id, coverage, declared_by
  FROM covers_subtree;

INSERT INTO doc_coverage_completed_with (edge_id, position, to_doc, to_external)
SELECT n.id, w.position, w.to_doc, w.to_external
  FROM covers_subtree m
  JOIN doc_edges n ON n.type = 'covers' AND n.from_doc = m.from_doc
                  AND n.from_anchor IS NOT DISTINCT FROM m.from_anchor
                  AND n.to_rule = m.rule_id
  JOIN doc_coverage_completed_with w ON w.edge_id = m.src_id;

COMMIT;
