-- The document this task is about (spec 025 §15.4): set on review tasks
-- minted at submission and design tasks minted at acceptance. Distinct
-- from plan_doc (the plan whose acceptance minted the task, 025 §9.2).
-- The §5 suppression guards are partial-index-backed queries over open
-- tasks carrying this reference — queries, not stored state (025 §1).
ALTER TABLE tasks ADD COLUMN about_doc bigint REFERENCES docs(id);
CREATE INDEX tasks_about_doc ON tasks (about_doc) WHERE about_doc IS NOT NULL;
