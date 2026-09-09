-- 029 §7.1 governed references: InsertGovernedRefs records, as
-- entity_edges rows, what a designated revision references. The endpoints
-- are governed-entity kinds (model.ApprovalEntityKinds: doc, deliverable,
-- task, pr), not entity_edges' original project/milestone -> deliverable/task
-- shape, and the rel is new. Widen the three CHECK constraints rather than
-- add a second table: the read pattern ("references touching this entity,
-- from either end") is the same one references.go already serves.
BEGIN;

ALTER TABLE entity_edges DROP CONSTRAINT entity_edges_from_kind_check;
ALTER TABLE entity_edges ADD CONSTRAINT entity_edges_from_kind_check
    CHECK (from_kind IN ('project', 'milestone', 'doc', 'deliverable', 'task', 'pr'));

ALTER TABLE entity_edges DROP CONSTRAINT entity_edges_to_kind_check;
ALTER TABLE entity_edges ADD CONSTRAINT entity_edges_to_kind_check
    CHECK (to_kind IN ('deliverable', 'task', 'doc', 'pr'));

ALTER TABLE entity_edges DROP CONSTRAINT entity_edges_rel_check;
ALTER TABLE entity_edges ADD CONSTRAINT entity_edges_rel_check
    CHECK (rel IN ('depends_on', 'seeded_by', 'references_revision'));

COMMIT;
