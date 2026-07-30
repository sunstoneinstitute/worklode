-- Task hierarchy (docs/specs/018-task-hierarchy.md): epics as declared
-- containers, at most one parent per task, indexed child lookups.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic'));

-- A task has at most one parent. Two child_of edges out of one task are legal
-- under the baseline UNIQUE (from_task, to_task, type), and the task page
-- silently keeps whichever was inserted last.
CREATE UNIQUE INDEX task_edges_single_parent
    ON task_edges (from_task) WHERE type = 'child_of';

-- Child lookups (WHERE to_task = $1 AND type = 'child_of') have no usable
-- index: the baseline unique constraint leads with from_task.
CREATE INDEX task_edges_children
    ON task_edges (to_task) WHERE type = 'child_of';
