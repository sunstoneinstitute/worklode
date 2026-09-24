BEGIN;

ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
UPDATE project_entity_seq SET kind = 'CL' WHERE kind = 'RULE';
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN', 'SPEC', 'ADR', 'MILE', 'CL'));

ALTER INDEX task_governed_by_rule RENAME TO task_governed_by_clause;
ALTER TABLE task_governed_by RENAME CONSTRAINT task_governed_by_rule_id_fkey TO task_governed_by_clause_id_fkey;
ALTER TABLE task_governed_by RENAME COLUMN rule_version TO clause_version;
ALTER TABLE task_governed_by RENAME COLUMN rule_id TO clause_id;

ALTER INDEX rule_edges_to RENAME TO clause_edges_to;
ALTER TABLE rule_edges RENAME CONSTRAINT rule_edges_source_check TO clause_edges_source_check;
ALTER TABLE rule_edges RENAME CONSTRAINT rule_edges_type_check TO clause_edges_type_check;
ALTER TABLE rule_edges RENAME CONSTRAINT rule_edges_to_rule_fkey TO clause_edges_to_clause_fkey;
ALTER TABLE rule_edges RENAME CONSTRAINT rule_edges_from_rule_fkey TO clause_edges_from_clause_fkey;
ALTER TABLE rule_edges RENAME CONSTRAINT rule_edges_check TO clause_edges_check;
ALTER TABLE rule_edges RENAME CONSTRAINT rule_edges_pkey TO clause_edges_pkey;
ALTER TABLE rule_edges RENAME COLUMN to_rule TO to_clause;
ALTER TABLE rule_edges RENAME COLUMN from_rule TO from_clause;
ALTER TABLE rule_edges RENAME TO clause_edges;

ALTER INDEX doc_rules_rule RENAME TO doc_clauses_clause;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_rules_rule_id_rule_version_fkey TO doc_clauses_clause_id_clause_version_fkey;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_rules_rule_id_fkey TO doc_clauses_clause_id_fkey;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_rules_doc_id_fkey TO doc_clauses_doc_id_fkey;
ALTER TABLE doc_rules RENAME CONSTRAINT doc_rules_pkey TO doc_clauses_pkey;
ALTER TABLE doc_rules RENAME COLUMN rule_version TO clause_version;
ALTER TABLE doc_rules RENAME COLUMN rule_id TO clause_id;
ALTER TABLE doc_rules RENAME TO doc_clauses;

ALTER TABLE rule_versions RENAME CONSTRAINT rule_versions_rule_id_fkey TO clause_versions_clause_id_fkey;
ALTER TABLE rule_versions RENAME CONSTRAINT rule_versions_pkey TO clause_versions_pkey;
ALTER TABLE rule_versions RENAME COLUMN rule_id TO clause_id;
ALTER TABLE rule_versions RENAME TO clause_versions;

ALTER TABLE rules RENAME CONSTRAINT rules_status_check TO clauses_status_check;
ALTER TABLE rules RENAME CONSTRAINT rules_project_id_number_key TO clauses_project_id_number_key;
ALTER TABLE rules RENAME CONSTRAINT rules_project_id_fkey TO clauses_project_id_fkey;
ALTER TABLE rules RENAME CONSTRAINT rules_pkey TO clauses_pkey;
ALTER SEQUENCE rules_id_seq RENAME TO clauses_id_seq;
ALTER TABLE rules RENAME TO clauses;

COMMIT;
