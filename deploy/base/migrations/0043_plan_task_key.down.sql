-- Reverse 0043_plan_task_key: drop the index, the constraint, the column.

DROP INDEX IF EXISTS tasks_plan_task_key;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_plan_task_key_with_plan_doc;
ALTER TABLE tasks DROP COLUMN plan_task_key;
