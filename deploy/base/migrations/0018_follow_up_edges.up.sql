-- The third task edge (004 §1.3): "A follow_up_to B" records that A was spun
-- out of the work on B. Provenance only -- it gates no claim and confers no
-- parent-hood, so no existing query changes: every one of them is already
-- qualified by edge type.
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;
ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('child_of','blocks','follow_up_to'));

-- A task has at most one origin, the way it has at most one parent. The origin
-- side is unbounded: one task spawns any number of follow-ups.
CREATE UNIQUE INDEX task_edges_single_origin
    ON task_edges (from_task) WHERE type = 'follow_up_to';
