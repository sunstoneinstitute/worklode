-- Spec 025 §7.3 / 029 §7.3: a document carries an assigned reviewer set and is
-- not accepted until *every* assigned reviewer approves. That needs one
-- 'awaiting' row per reviewer lane on the same revision, which 0038's
-- UNIQUE (entity_kind, entity_id, subject_revision) forbids. The lane columns
-- join the key.
--
-- NULLS NOT DISTINCT is load-bearing: both lane columns are nullable, and the
-- SQL default counts every NULL as distinct, so an unqualified lane (both
-- NULL — what the PR ingest writes) would no longer be deduplicated and a
-- redelivered webhook would insert a second row.
--
-- entity_kind stays unconstrained text; 'doc' is simply the second value now
-- written. 'deliverable' waits on a revision concept: deliverables (0015)
-- carry no version or revision column, so their subject_revision is undefined.
ALTER TABLE approvals
    DROP CONSTRAINT approvals_entity_kind_entity_id_subject_revision_key;

ALTER TABLE approvals
    ADD CONSTRAINT approvals_lane_key UNIQUE NULLS NOT DISTINCT
        (entity_kind, entity_id, subject_revision, required_role, required_actor);
