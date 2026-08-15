-- A merge reported by a developer's own clone (`lode hook post-merge`) is a
-- weaker claim than a signed webhook, so it gets its own source value rather
-- than borrowing 'merge_message'. The log says which reporter asserted the
-- delivery.
ALTER TABLE task_commits DROP CONSTRAINT task_commits_source_check;
ALTER TABLE task_commits ADD CONSTRAINT task_commits_source_check
    CHECK (source IN ('branch_push','pr','merge_message','marker','local_merge'));
