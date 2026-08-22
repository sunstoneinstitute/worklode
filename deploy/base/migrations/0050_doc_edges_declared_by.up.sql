-- Which document's frontmatter put an edge row here (025 §5).
--
-- Until now the writer and the from end were the same document, so
-- rebuildEdges could clear a document's declarations with
-- `DELETE ... WHERE from_doc = $1`. A plan's `blockedBy:` writes the same
-- `blocks` row with its ends swapped, which puts the row's from end on the
-- *other* plan — so the two questions come apart: from_doc is where the
-- relation points from, declared_by is whose frontmatter is answerable for
-- the row existing.
--
-- Backfilled to from_doc, which is what every existing row means.

ALTER TABLE doc_edges
    ADD COLUMN declared_by bigint REFERENCES docs(id) ON DELETE CASCADE;

UPDATE doc_edges SET declared_by = from_doc;

ALTER TABLE doc_edges ALTER COLUMN declared_by SET NOT NULL;

CREATE INDEX doc_edges_declared_by ON doc_edges (declared_by);
