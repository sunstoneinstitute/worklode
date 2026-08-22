DELETE FROM doc_edges WHERE type = 'defers';
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_defers_from_doc;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_type_check;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_type_check CHECK (type IN
    ('covers','implements','amends','replaces','requires','wasDerivedFrom','blocks'));
