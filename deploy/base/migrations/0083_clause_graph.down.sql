ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_pinned_fkey;
ALTER TABLE task_governed_by DROP COLUMN pinned_version;
ALTER TABLE clauses DROP COLUMN tags;
ALTER TABLE clauses DROP COLUMN owner;
DROP TABLE clause_edges;
