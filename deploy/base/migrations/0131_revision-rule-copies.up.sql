-- WL-REQ-1300: a candidate revision records the rule text it copied, so
-- accepting it can tell a stale copy (the rule was edited since) from an edit
-- made in the candidate. Open candidates take the text at their arranged
-- version, the best record there is.
ALTER TABLE doc_revision_rules ADD COLUMN copied_heading text, ADD COLUMN copied_body text;

UPDATE doc_revision_rules r SET copied_heading = v.heading, copied_body = v.body
  FROM rule_versions v
 WHERE v.rule_id = r.rule_id AND v.version = r.rule_version;
