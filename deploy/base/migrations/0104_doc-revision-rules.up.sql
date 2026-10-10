-- A candidate revision carries its own arrangement (WL-SPEC-77 §19.3), shaped
-- like doc_rules, so arranging or unarranging a rule in an accepted spec
-- lands with the revision. Open candidates start from their document's
-- current arrangement.
BEGIN;

CREATE TABLE doc_revision_rules (
    doc_id        bigint  NOT NULL REFERENCES doc_revisions(doc_id) ON DELETE CASCADE,
    position      integer NOT NULL,
    rule_id       bigint  REFERENCES rules(id),
    rule_version  integer,
    heading       text,
    depth         integer NOT NULL,
    anchor        text    NOT NULL,
    PRIMARY KEY (doc_id, position),
    FOREIGN KEY (rule_id, rule_version) REFERENCES rule_versions(rule_id, version),
    CONSTRAINT doc_revision_rules_rule_or_heading
        CHECK ((rule_id IS NULL) <> (heading IS NULL) AND (rule_id IS NULL) = (rule_version IS NULL))
);
CREATE INDEX doc_revision_rules_rule ON doc_revision_rules (rule_id);

INSERT INTO doc_revision_rules (doc_id, position, rule_id, rule_version, heading, depth, anchor)
SELECT r.doc_id, r.position, r.rule_id, r.rule_version, r.heading, r.depth, r.anchor
  FROM doc_rules r JOIN doc_revisions v ON v.doc_id = r.doc_id;

COMMIT;
