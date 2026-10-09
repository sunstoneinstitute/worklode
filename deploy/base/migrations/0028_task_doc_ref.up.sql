-- The document this task is about (WL-SPEC-77): set on review tasks
-- minted at submission and design tasks minted at acceptance. Distinct
-- from plan_doc (the plan whose acceptance minted the task, WL-SPEC-77).
-- The §5 suppression guards are partial-index-backed queries over open
-- tasks carrying this reference — queries, not stored state (WL-SPEC-77).
ALTER TABLE tasks ADD COLUMN about_doc bigint REFERENCES docs(id);
CREATE INDEX tasks_about_doc ON tasks (about_doc) WHERE about_doc IS NOT NULL;
