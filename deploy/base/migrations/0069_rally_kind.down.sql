-- Reverse 0069: a down is destructive by definition, so any rally-kind rows
-- go with it. Drop the index before the rows so the index's own predicate
-- never has to evaluate against a kind the CHECK is about to disallow.

DROP INDEX tasks_one_open_rally;
DELETE FROM tasks WHERE kind = 'rally';

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'));
