-- Restores the looser CHECK only. The amends and replaces rows the up
-- migration deleted are not restored.
BEGIN;

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_type_check;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_type_check CHECK (type IN
    ('covers', 'implements', 'amends', 'replaces', 'requires', 'wasDerivedFrom', 'blocks', 'defers'));

COMMIT;
