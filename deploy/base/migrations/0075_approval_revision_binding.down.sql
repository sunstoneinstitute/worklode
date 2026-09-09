BEGIN;

ALTER TABLE approvals
    DROP CONSTRAINT approvals_entity_revision_lane_review_kind_key;
ALTER TABLE approvals
    ADD CONSTRAINT approvals_entity_revision_lane_key
        UNIQUE (entity_kind, entity_id, subject_revision, lane);

ALTER TABLE approvals DROP COLUMN exception_authorized_by;
ALTER TABLE approvals DROP COLUMN note;
ALTER TABLE approvals DROP COLUMN review_kind;

COMMIT;
