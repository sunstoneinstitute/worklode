-- 029 §7.1 revision binding. An impact review is an approvals row too, so
-- "what is waiting on whom" stays one query over rows that exist.
-- review_kind separates an ordinary review from a dependent-object impact
-- review; note carries the downstream owner's impact note (§7.1: "the
-- downstream owner supplies an impact note"); exception_authorized_by is
-- the actor who approved a policy-permitted self-review before review —
-- one of the two facts 032 §7 renders beside the decision.
BEGIN;

ALTER TABLE approvals ADD COLUMN review_kind text NOT NULL DEFAULT 'review'
    CHECK (review_kind IN ('review', 'impact'));
ALTER TABLE approvals ADD COLUMN note text;
ALTER TABLE approvals ADD COLUMN exception_authorized_by text
    REFERENCES actors (id) ON DELETE RESTRICT;

ALTER TABLE approvals
    DROP CONSTRAINT approvals_entity_revision_lane_key;
ALTER TABLE approvals
    ADD CONSTRAINT approvals_entity_revision_lane_review_kind_key
        UNIQUE (entity_kind, entity_id, subject_revision, lane, review_kind);

COMMIT;
