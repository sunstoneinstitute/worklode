-- A superseded document or rule version keeps its edges and its arrangement
-- (WL-SPEC-77 §3). bumpDocVersion and bumpRuleVersion (internal/store
-- versions.go) copy the live rows here before moving the version. No type
-- CHECK: a snapshot keeps what was valid when it was taken.
BEGIN;

CREATE TABLE doc_edge_versions (
    doc_id         bigint  NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    version        integer NOT NULL,
    from_anchor    text,
    type           text    NOT NULL,
    to_doc         bigint,
    to_anchor      text,
    to_external    text,
    coverage       text,
    to_rule        bigint,
    -- doc_coverage_completed_with in position order: {"to_doc": id} or
    -- {"to_external": "ref"}.
    completed_with jsonb
);
CREATE INDEX doc_edge_versions_doc ON doc_edge_versions (doc_id, version);

CREATE TABLE doc_rule_versions (
    doc_id       bigint  NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    version      integer NOT NULL,
    position     integer NOT NULL,
    rule_id      bigint  NOT NULL REFERENCES rules(id),
    rule_version integer NOT NULL,
    depth        integer NOT NULL,
    anchor       text    NOT NULL,
    PRIMARY KEY (doc_id, version, position),
    FOREIGN KEY (rule_id, rule_version) REFERENCES rule_versions(rule_id, version)
);

CREATE TABLE rule_edge_versions (
    rule_id bigint  NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    version integer NOT NULL,
    to_rule bigint  NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    type    text    NOT NULL,
    source  text    NOT NULL,
    PRIMARY KEY (rule_id, version, to_rule, type)
);

COMMIT;
