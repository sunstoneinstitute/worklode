-- Rows recorded by the local-merge reporter would violate the narrowed CHECK,
-- so fold them into the closest webhook-era source before restoring it. The
-- attribution survives; only the provenance of who asserted it is lost.
UPDATE task_commits SET source = 'merge_message' WHERE source = 'local_merge';
ALTER TABLE task_commits DROP CONSTRAINT task_commits_source_check;
ALTER TABLE task_commits ADD CONSTRAINT task_commits_source_check
    CHECK (source IN ('branch_push','pr','merge_message','marker'));
