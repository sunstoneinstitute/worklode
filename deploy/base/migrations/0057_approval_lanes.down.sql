-- Lossy when multi-lane rows exist: restoring the narrower key fails on any
-- revision that already carries more than one lane row. Delete the surplus
-- rows before running this.
ALTER TABLE approvals DROP CONSTRAINT approvals_lane_key;

ALTER TABLE approvals
    ADD CONSTRAINT approvals_entity_kind_entity_id_subject_revision_key
        UNIQUE (entity_kind, entity_id, subject_revision);
