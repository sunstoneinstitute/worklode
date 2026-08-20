DROP TABLE doc_coverage_completed_with;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_coverage_on_covers;
ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_coverage_level;
ALTER TABLE doc_edges DROP COLUMN coverage;
