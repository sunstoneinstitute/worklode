-- Documents no longer amend or replace each other (WL-SPEC-77 §8):
-- amendment and supersession are rule_edges rows. The document-level rows go,
-- and the doc_edges type CHECK stops admitting them. doc_coverage_completed_with
-- cascades off doc_edges.
BEGIN;

DELETE FROM doc_edges WHERE type IN ('amends', 'replaces');

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_type_check;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_type_check CHECK (type IN
    ('covers', 'implements', 'requires', 'wasDerivedFrom', 'blocks', 'defers'));

COMMIT;
