-- The conflictsWith pairs deleted by the up migration are not restored: one
-- row per pair already states the symmetric relation.
BEGIN;

DROP INDEX rule_edges_symmetric_once;

ALTER TABLE doc_edges ADD COLUMN declared_by bigint REFERENCES docs(id) ON DELETE CASCADE;
UPDATE doc_edges SET declared_by = from_doc;
ALTER TABLE doc_edges ALTER COLUMN declared_by SET NOT NULL;
CREATE INDEX doc_edges_declared_by ON doc_edges (declared_by);

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_blocked_by_whole_docs;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_type_check;

UPDATE doc_edges SET from_doc = to_doc, to_doc = from_doc, type = 'blocks'
 WHERE type = 'blockedBy';

ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_type_check CHECK (type IN
    ('covers', 'implements', 'requires', 'wasDerivedFrom', 'blocks', 'defers'));
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_check1
    CHECK (type <> 'blocks' OR (from_anchor IS NULL AND to_anchor IS NULL));

COMMIT;
