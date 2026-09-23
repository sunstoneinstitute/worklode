-- The delete discards every governing link the gate wrote. Re-applying the
-- up migration does not recreate them: the spec-reconciler subscriber's
-- offset has moved past the events those links came from.
DELETE FROM task_governed_by WHERE source = 'gate';
ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_source_check;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_source_check
    CHECK (source IN ('plan', 'manual'));
