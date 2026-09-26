-- Every edge row is owned by its from end (WL-SPEC-77 §8). Plan ordering is
-- stored as blockedBy, written by the blocked plan, so declared_by no longer
-- differs from from_doc and goes. conflictsWith is symmetric and is stored
-- once per pair.
BEGIN;

DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM doc_edges WHERE type = 'blocks' AND to_doc IS NULL) THEN
    RAISE EXCEPTION 'doc_edges holds an unresolved blocks row; resolve or delete it first';
  END IF;
END $$;

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_type_check;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_check1; -- 0027's "blocks is never section-scoped"

UPDATE doc_edges SET from_doc = to_doc, to_doc = from_doc, type = 'blockedBy'
 WHERE type = 'blocks';

ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_type_check CHECK (type IN
    ('covers', 'implements', 'requires', 'wasDerivedFrom', 'blockedBy', 'defers'));
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_blocked_by_whole_docs
    CHECK (type <> 'blockedBy' OR (from_anchor IS NULL AND to_anchor IS NULL));

DROP INDEX doc_edges_declared_by;
ALTER TABLE doc_edges DROP COLUMN declared_by;

DELETE FROM rule_edges a USING rule_edges b
 WHERE a.type = 'conflictsWith' AND b.type = 'conflictsWith'
   AND a.from_rule = b.to_rule AND a.to_rule = b.from_rule AND a.from_rule > a.to_rule;
CREATE UNIQUE INDEX rule_edges_symmetric_once
    ON rule_edges (least(from_rule, to_rule), greatest(from_rule, to_rule), type)
 WHERE type = 'conflictsWith';

COMMIT;
