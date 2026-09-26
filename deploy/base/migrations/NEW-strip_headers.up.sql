-- Stored bodies carry no YAML header (WL-SPEC-77 §7): title and issued are
-- docs columns and edges are doc_edges rows, so the header only restated
-- them. doc_versions is left verbatim: history keeps the text as written.
BEGIN;

UPDATE docs SET body = ltrim(substr(body, 4 + position(E'\n---\n' IN substr(body, 4)) + 4), E'\n')
 WHERE body LIKE E'---\n%' AND position(E'\n---\n' IN substr(body, 4)) > 0;
UPDATE doc_revisions SET body = ltrim(substr(body, 4 + position(E'\n---\n' IN substr(body, 4)) + 4), E'\n')
 WHERE body LIKE E'---\n%' AND position(E'\n---\n' IN substr(body, 4)) > 0;

COMMIT;
