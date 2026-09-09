BEGIN;

ALTER TABLE entity_edges DROP CONSTRAINT entity_edges_rel_check;
ALTER TABLE entity_edges ADD CONSTRAINT entity_edges_rel_check
    CHECK (rel IN ('depends_on', 'seeded_by'));

ALTER TABLE entity_edges DROP CONSTRAINT entity_edges_to_kind_check;
ALTER TABLE entity_edges ADD CONSTRAINT entity_edges_to_kind_check
    CHECK (to_kind IN ('deliverable', 'task'));

ALTER TABLE entity_edges DROP CONSTRAINT entity_edges_from_kind_check;
ALTER TABLE entity_edges ADD CONSTRAINT entity_edges_from_kind_check
    CHECK (from_kind IN ('project', 'milestone'));

COMMIT;
