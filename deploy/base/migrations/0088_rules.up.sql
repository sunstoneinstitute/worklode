-- The design clause is renamed rule (docs/specs2/12-spec-refactoring-design-tree.md
-- S64): tables, columns, indexes, constraints and the sequence follow, and the
-- project counter kind 'CL' becomes 'RULE'. Event rows keep their old names.
BEGIN;

ALTER TABLE clauses RENAME TO rules;
ALTER SEQUENCE clauses_id_seq RENAME TO rules_id_seq;
ALTER TABLE rules RENAME CONSTRAINT clauses_pkey TO rules_pkey;
ALTER TABLE rules RENAME CONSTRAINT clauses_project_id_fkey TO rules_project_id_fkey;
ALTER TABLE rules RENAME CONSTRAINT clauses_project_id_number_key TO rules_project_id_number_key;
ALTER TABLE rules RENAME CONSTRAINT clauses_status_check TO rules_status_check;

ALTER TABLE clause_versions RENAME TO rule_versions;
ALTER TABLE rule_versions RENAME COLUMN clause_id TO rule_id;
ALTER TABLE rule_versions RENAME CONSTRAINT clause_versions_pkey TO rule_versions_pkey;
ALTER TABLE rule_versions RENAME CONSTRAINT clause_versions_clause_id_fkey TO rule_versions_rule_id_fkey;

ALTER TABLE doc_clauses RENAME TO doc_rules;
ALTER TABLE doc_rules RENAME COLUMN clause_id TO rule_id;
ALTER TABLE doc_rules RENAME COLUMN clause_version TO rule_version;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_clauses_pkey TO doc_rules_pkey;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_clauses_doc_id_fkey TO doc_rules_doc_id_fkey;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_clauses_clause_id_fkey TO doc_rules_rule_id_fkey;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_clauses_clause_id_clause_version_fkey TO doc_rules_rule_id_rule_version_fkey;
ALTER INDEX doc_clauses_clause RENAME TO doc_rules_rule;

ALTER TABLE clause_edges RENAME TO rule_edges;
ALTER TABLE rule_edges RENAME COLUMN from_clause TO from_rule;
ALTER TABLE rule_edges RENAME COLUMN to_clause TO to_rule;
ALTER TABLE rule_edges RENAME CONSTRAINT clause_edges_pkey TO rule_edges_pkey;
ALTER TABLE rule_edges RENAME CONSTRAINT clause_edges_check TO rule_edges_check;
ALTER TABLE rule_edges RENAME CONSTRAINT clause_edges_from_clause_fkey TO rule_edges_from_rule_fkey;
ALTER TABLE rule_edges RENAME CONSTRAINT clause_edges_to_clause_fkey TO rule_edges_to_rule_fkey;
ALTER TABLE rule_edges RENAME CONSTRAINT clause_edges_type_check TO rule_edges_type_check;
ALTER TABLE rule_edges RENAME CONSTRAINT clause_edges_source_check TO rule_edges_source_check;
ALTER INDEX clause_edges_to RENAME TO rule_edges_to;

ALTER TABLE task_governed_by RENAME COLUMN clause_id TO rule_id;
ALTER TABLE task_governed_by RENAME COLUMN clause_version TO rule_version;
ALTER TABLE task_governed_by RENAME CONSTRAINT task_governed_by_clause_id_fkey TO task_governed_by_rule_id_fkey;
ALTER INDEX task_governed_by_clause RENAME TO task_governed_by_rule;

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
UPDATE project_entity_seq SET kind = 'RULE' WHERE kind = 'CL';
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR', 'MILE', 'RULE'));

COMMIT;
