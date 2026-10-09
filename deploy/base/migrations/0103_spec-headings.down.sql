BEGIN;

DELETE FROM doc_rule_versions WHERE rule_id IS NULL;
ALTER TABLE doc_rule_versions DROP CONSTRAINT doc_rule_versions_rule_or_heading;
ALTER TABLE doc_rule_versions ALTER COLUMN rule_version SET NOT NULL;
ALTER TABLE doc_rule_versions ALTER COLUMN rule_id SET NOT NULL;
ALTER TABLE doc_rule_versions DROP COLUMN heading;

DELETE FROM doc_rules WHERE rule_id IS NULL;
ALTER TABLE doc_rules DROP CONSTRAINT doc_rules_rule_or_heading;
ALTER TABLE doc_rules ALTER COLUMN rule_version SET NOT NULL;
ALTER TABLE doc_rules ALTER COLUMN rule_id SET NOT NULL;
ALTER TABLE doc_rules DROP COLUMN heading;

COMMIT;
