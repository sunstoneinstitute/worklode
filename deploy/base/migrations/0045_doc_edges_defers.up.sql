-- 0045: admit the 'defers' edge type — a plan's explicit handoff of a spec
-- section to a named owner (026 §5.3). The owner rides in
-- doc_coverage_completed_with, the completion side-table a partial covers
-- entry already uses, so no new table is minted.
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_type_check;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_type_check CHECK (type IN
    ('covers','implements','amends','replaces','requires','wasDerivedFrom','blocks','defers'));
-- A plan has no sections (025 §9), so a defers edge never leaves from an anchor.
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_defers_from_doc
    CHECK (type <> 'defers' OR from_anchor IS NULL);
