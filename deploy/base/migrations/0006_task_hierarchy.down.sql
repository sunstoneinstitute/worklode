DROP INDEX task_edges_children;
DROP INDEX task_edges_single_parent;

-- Re-adding the four-kind CHECK validates existing rows, so the revert fails
-- loudly if any epic survives rather than leaving an unrepresentable task;
-- an operator must reassign or abandon the surviving epics, then re-run.
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec'));
