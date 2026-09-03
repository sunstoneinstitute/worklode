-- Reverse 0066: a down is destructive by definition, so any decision-kind
-- rows go with it. task_decisions is FK'd to tasks and drops first.

DELETE FROM tasks WHERE kind = 'decision';
DROP TABLE task_decisions;

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike'));
