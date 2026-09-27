-- Lossy: the columns come back with defaults. Every covers edge is 'full';
-- the edges the up migration deleted and the levels it dropped are not
-- restored. Defers owners move back into doc_coverage_completed_with at
-- position 0.
BEGIN;

DROP VIEW covered_sections;
DROP VIEW covered_rules;

ALTER TABLE doc_edges ADD COLUMN coverage text;
UPDATE doc_edges SET coverage = 'full' WHERE type = 'covers';
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_coverage_level
    CHECK (coverage IS NULL OR coverage IN ('full','partial','none'));
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_coverage_on_covers
    CHECK ((type = 'covers') = (coverage IS NOT NULL));

CREATE TABLE doc_coverage_completed_with (
    edge_id     bigint NOT NULL REFERENCES doc_edges(id) ON DELETE CASCADE,
    position    integer NOT NULL,
    to_doc      bigint REFERENCES docs(id),
    to_external text,
    PRIMARY KEY (edge_id, position),
    CHECK ((to_doc IS NULL) <> (to_external IS NULL))
);
INSERT INTO doc_coverage_completed_with (edge_id, position, to_doc, to_external)
SELECT id, 0, owner_doc, owner_external FROM doc_edges
 WHERE type = 'defers' AND num_nonnulls(owner_doc, owner_external) = 1;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_owner_on_defers;
ALTER TABLE doc_edges DROP COLUMN owner_doc, DROP COLUMN owner_external;

ALTER TABLE doc_revision_edges ADD COLUMN coverage text, ADD COLUMN completed_with jsonb;
UPDATE doc_revision_edges SET coverage = 'full' WHERE type = 'covers';
UPDATE doc_revision_edges
   SET completed_with = jsonb_build_array(CASE WHEN owner_doc IS NOT NULL
                                               THEN jsonb_build_object('to_doc', owner_doc)
                                               ELSE jsonb_build_object('to_external', owner_external) END)
 WHERE type = 'defers' AND num_nonnulls(owner_doc, owner_external) = 1;
ALTER TABLE doc_revision_edges ADD CONSTRAINT doc_revision_edges_coverage_on_covers
    CHECK ((type = 'covers') = (coverage IS NOT NULL));
ALTER TABLE doc_revision_edges ADD CONSTRAINT doc_revision_edges_coverage_level
    CHECK (coverage IS NULL OR coverage IN ('full', 'partial', 'none'));
ALTER TABLE doc_revision_edges DROP CONSTRAINT doc_revision_edges_owner_on_defers;
ALTER TABLE doc_revision_edges DROP COLUMN owner_doc, DROP COLUMN owner_external;

ALTER TABLE doc_edge_versions ADD COLUMN coverage text, ADD COLUMN completed_with jsonb;
UPDATE doc_edge_versions SET coverage = 'full' WHERE type = 'covers';
UPDATE doc_edge_versions
   SET completed_with = jsonb_build_array(CASE WHEN owner_doc IS NOT NULL
                                               THEN jsonb_build_object('to_doc', owner_doc)
                                               ELSE jsonb_build_object('to_external', owner_external) END)
 WHERE type = 'defers' AND num_nonnulls(owner_doc, owner_external) = 1;
ALTER TABLE doc_edge_versions DROP COLUMN owner_doc, DROP COLUMN owner_external;

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

CREATE VIEW covered_sections AS
SELECT c.edge_id, c.plan_id, c.rule_id, c.coverage, dr.doc_id, dr.anchor
  FROM covered_rules c
  JOIN doc_rules dr ON dr.rule_id = c.rule_id;

COMMIT;
