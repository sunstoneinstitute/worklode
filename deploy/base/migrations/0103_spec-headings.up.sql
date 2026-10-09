-- Spec headings (WL-SPEC-77 §19.1): an arrangement entry is either a rule or a
-- heading the spec owns, which carries heading text and no rule.
BEGIN;

ALTER TABLE doc_rules ADD COLUMN heading text;
ALTER TABLE doc_rules ALTER COLUMN rule_id DROP NOT NULL;
ALTER TABLE doc_rules ALTER COLUMN rule_version DROP NOT NULL;
ALTER TABLE doc_rules ADD CONSTRAINT doc_rules_rule_or_heading
    CHECK ((rule_id IS NULL) <> (heading IS NULL) AND (rule_id IS NULL) = (rule_version IS NULL));

ALTER TABLE doc_rule_versions ADD COLUMN heading text;
ALTER TABLE doc_rule_versions ALTER COLUMN rule_id DROP NOT NULL;
ALTER TABLE doc_rule_versions ALTER COLUMN rule_version DROP NOT NULL;
ALTER TABLE doc_rule_versions ADD CONSTRAINT doc_rule_versions_rule_or_heading
    CHECK ((rule_id IS NULL) <> (heading IS NULL) AND (rule_id IS NULL) = (rule_version IS NULL));

COMMIT;
