-- Reverse the schema half of derived section numbers. The renumbering is
-- one-way; each entry's stored anchor is refilled from the section index.
BEGIN;

CREATE OR REPLACE VIEW covered_sections AS
SELECT c.edge_id, c.plan_id, c.rule_id, dr.doc_id, dr.anchor
  FROM covered_rules c
  JOIN doc_rules dr ON dr.rule_id = c.rule_id;
DROP VIEW doc_entries;

UPDATE doc_rules dr SET anchor = s.anchor
  FROM doc_sections s WHERE s.doc_id = dr.doc_id AND s.position = dr.position;
UPDATE doc_rules SET anchor = coalesce(slug, 'sec-' || doc_id || '-' || position) WHERE anchor IS NULL;
UPDATE doc_rule_versions SET anchor = coalesce(slug, 'sec-' || position) WHERE anchor IS NULL;
UPDATE doc_revision_rules SET anchor = coalesce(slug, 'sec-' || position) WHERE anchor IS NULL;
ALTER TABLE doc_rules ALTER COLUMN anchor SET NOT NULL;
ALTER TABLE doc_rule_versions ALTER COLUMN anchor SET NOT NULL;
ALTER TABLE doc_revision_rules ALTER COLUMN anchor SET NOT NULL;
ALTER TABLE doc_rules DROP COLUMN slug;
ALTER TABLE doc_rule_versions DROP COLUMN slug;
ALTER TABLE doc_revision_rules DROP COLUMN slug;

COMMIT;
