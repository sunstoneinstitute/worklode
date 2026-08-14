-- Restore the wider CHECK of 0009_task_kinds verbatim. Widening a CHECK always
-- succeeds: every row legal under the six kinds is legal under the seven.
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic','review','spike'));
