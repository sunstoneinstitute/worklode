-- Spec 029 §7.2: one revision carries several independent review lanes
-- (Science Lead and domain-expert on the same methodology revision are two
-- rows). lane names the flow requirement that minted the row; part 1's
-- PR rows and ad-hoc rows that name no lane keep ''.
ALTER TABLE approvals ADD COLUMN lane text NOT NULL DEFAULT '';

-- Who put the requirement here. Rule-created rows are owned by the system
-- 'worklode' actor (029 §7.2); ad-hoc rows record the requesting actor.
-- Nullable: part 1's ingest rows predate the column.
ALTER TABLE approvals ADD COLUMN created_by text
    REFERENCES actors (id) ON DELETE RESTRICT;

-- Supersedes 0057's required_role/required_actor key: lane now names the
-- dimension a row is unique on, so the key is keyed on lane instead.
ALTER TABLE approvals
    DROP CONSTRAINT approvals_lane_key;
ALTER TABLE approvals ADD CONSTRAINT approvals_entity_revision_lane_key
    UNIQUE (entity_kind, entity_id, subject_revision, lane);
