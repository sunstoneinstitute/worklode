-- WL-1086: doc_rules.anchor and doc_rule_versions.anchor stopped being written
-- in 0107 (WL-1081); anchors derive from doc_sections. doc_revision_rules.anchor stays.
ALTER TABLE doc_rules DROP COLUMN anchor;
ALTER TABLE doc_rule_versions DROP COLUMN anchor;
