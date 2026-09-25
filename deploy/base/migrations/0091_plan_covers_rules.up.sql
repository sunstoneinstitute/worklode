-- A plan's covers edge runs from the plan to a rule (WL-SPEC-77 §3, §4, §8).
-- A plan contains no rules, so its doc_rules rows go. Existing covers edges
-- are resolved the way a plan write resolves them: a section-scoped edge to
-- the rule at that anchor, a whole-document edge to every rule the document
-- contains. An edge whose target holds no such rule keeps its reference in
-- to_external.
BEGIN;

ALTER TABLE doc_edges ADD COLUMN to_rule bigint REFERENCES rules(id);

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_check;
DROP INDEX doc_edges_unique;

-- One row per (plan, rule). Where two old edges reach the same rule, the
-- section-scoped one wins over the whole-document one, then the older one.
CREATE TEMP TABLE covers_move ON COMMIT DROP AS
SELECT DISTINCT ON (e.from_doc, coalesce(e.from_anchor, ''), dr.rule_id)
       e.id AS old_id, e.from_doc, e.from_anchor, dr.rule_id, e.coverage
  FROM doc_edges e
  JOIN docs t ON t.id = e.to_doc AND t.kind <> 'plan'
  JOIN doc_rules dr ON dr.doc_id = e.to_doc
                   AND (e.to_anchor IS NULL OR dr.anchor = e.to_anchor)
 WHERE e.type = 'covers'
 ORDER BY e.from_doc, coalesce(e.from_anchor, ''), dr.rule_id, (e.to_anchor IS NULL), e.id;

INSERT INTO doc_edges (from_doc, from_anchor, type, to_rule, coverage, declared_by)
SELECT from_doc, from_anchor, 'covers', rule_id, coverage, from_doc
  FROM covers_move;

INSERT INTO doc_coverage_completed_with (edge_id, position, to_doc, to_external)
SELECT n.id, w.position, w.to_doc, w.to_external
  FROM covers_move m
  JOIN doc_edges n ON n.type = 'covers' AND n.from_doc = m.from_doc
                  AND n.from_anchor IS NOT DISTINCT FROM m.from_anchor
                  AND n.to_rule = m.rule_id
  JOIN doc_coverage_completed_with w ON w.edge_id = m.old_id;

DELETE FROM doc_edges e
 WHERE e.type = 'covers' AND e.to_doc IS NOT NULL
   AND EXISTS (SELECT 1 FROM doc_rules dr JOIN docs t ON t.id = dr.doc_id AND t.kind <> 'plan'
                WHERE dr.doc_id = e.to_doc
                  AND (e.to_anchor IS NULL OR dr.anchor = e.to_anchor));

UPDATE doc_edges e
   SET to_external = CASE WHEN t.number IS NULL THEN t.slug
                          ELSE p.key || '-' || upper(t.kind) || '-' || t.number END
                     || coalesce('#' || e.to_anchor, ''),
       to_doc = NULL, to_anchor = NULL
  FROM docs t JOIN projects p ON p.id = t.project_id
 WHERE e.type = 'covers' AND e.to_doc = t.id;

ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_one_target
    CHECK (num_nonnulls(to_doc, to_rule, to_external) = 1);
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_covers_rule
    CHECK (CASE WHEN type = 'covers' THEN to_doc IS NULL AND to_anchor IS NULL
                ELSE to_rule IS NULL END);
CREATE UNIQUE INDEX doc_edges_unique ON doc_edges
    (from_doc, coalesce(from_anchor, ''), type, coalesce(to_doc, 0), coalesce(to_rule, 0),
     coalesce(to_anchor, ''), coalesce(to_external, ''));
CREATE INDEX doc_edges_to_rule ON doc_edges (to_rule) WHERE to_rule IS NOT NULL;

DELETE FROM doc_rules dr USING docs d WHERE d.id = dr.doc_id AND d.kind = 'plan';

-- covered_rules is every rule a covers edge reaches: its to_rule and,
-- transitively, each rule that supersedes a reached rule, so a successor
-- counts as covered when a predecessor was (WL-SPEC-77 §4). This is the one
-- place coverage follows supersession; every coverage read goes through it.
-- supersedes runs new -> old (from_rule is the successor).
CREATE VIEW covered_rules AS
WITH RECURSIVE walk (edge_id, plan_id, rule_id, coverage) AS (
    SELECT id, from_doc, to_rule, coverage
      FROM doc_edges
     WHERE type = 'covers' AND to_rule IS NOT NULL
  UNION
    SELECT w.edge_id, w.plan_id, s.from_rule, w.coverage
      FROM walk w
      JOIN rule_edges s ON s.to_rule = w.rule_id AND s.type = 'supersedes'
)
SELECT edge_id, plan_id, rule_id, coverage FROM walk;

-- covered_sections places each covered rule at every section arranging it.
CREATE VIEW covered_sections AS
SELECT c.edge_id, c.plan_id, c.rule_id, c.coverage, dr.doc_id, dr.anchor
  FROM covered_rules c
  JOIN doc_rules dr ON dr.rule_id = c.rule_id;

COMMIT;
