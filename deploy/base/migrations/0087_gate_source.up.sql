-- The design authority gate is the third writer of a task's governing links
-- (11-design-authority-gate.md §4, 12-spec-refactoring-design-tree.md S4,
-- S46): a Spec: trailer on a planless task's pull request or push.
ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_source_check;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_source_check
    CHECK (source IN ('plan', 'manual', 'gate'));
