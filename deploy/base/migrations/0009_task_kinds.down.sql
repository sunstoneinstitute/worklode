-- Re-adding the five-kind CHECK validates existing rows, so the revert fails
-- loudly if any review or spike task survives rather than leaving an
-- unrepresentable task; an operator must re-kind those tasks, then re-run.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic'));
