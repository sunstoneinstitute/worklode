-- The fourth task edge (004 §1.3): "A duplicate_of B" records that A is the
-- same request as B, which is the canonical one. Provenance only, exactly like
-- follow_up_to -- it gates no claim, confers no parent-hood, and absorbs
-- nothing from A into B, so no existing query changes: every one of them is
-- already qualified by edge type.
ALTER TABLE task_edges DROP CONSTRAINT task_edges_type_check;
ALTER TABLE task_edges ADD CONSTRAINT task_edges_type_check
    CHECK (type IN ('child_of','blocks','follow_up_to','duplicate_of'));

-- A duplicate has exactly one canonical task, the way a follow-up has one
-- origin. The canonical side is unbounded: one task absorbs any number of
-- duplicates.
CREATE UNIQUE INDEX task_edges_single_canonical
    ON task_edges (from_task) WHERE type = 'duplicate_of';
