-- The design authority gate is the third writer of a task's governing links
-- (WL-REQ-7, WL-REQ-110): a Spec: trailer on a planless task's pull request or push.
ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_source_check;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_source_check
    CHECK (source IN ('plan', 'manual', 'gate'));
