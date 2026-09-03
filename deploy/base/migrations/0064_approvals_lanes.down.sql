-- Lossy when multi-lane rows exist: restoring the narrower key fails on any
-- revision that already carries more than one lane row. Delete the surplus
-- rows before running this.
ALTER TABLE approvals DROP CONSTRAINT approvals_entity_revision_lane_key;

ALTER TABLE approvals
    ADD CONSTRAINT approvals_lane_key UNIQUE NULLS NOT DISTINCT
        (entity_kind, entity_id, subject_revision, required_role, required_actor);

ALTER TABLE approvals DROP COLUMN created_by;
ALTER TABLE approvals DROP COLUMN lane;
