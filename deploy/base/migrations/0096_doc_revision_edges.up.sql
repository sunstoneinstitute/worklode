-- An open candidate revision carries its own edge set (WL-SPEC-77 §3), so a
-- relation can change in a revision without the body's header. The checks
-- and foreign keys mirror doc_edges, so landing a candidate cannot fail on
-- them. completed_with is doc_coverage_completed_with in position order:
-- {"to_doc": id} or {"to_external": "ref"}.
BEGIN;

CREATE TABLE doc_revision_edges (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_id         bigint NOT NULL REFERENCES doc_revisions(doc_id) ON DELETE CASCADE,
    from_anchor    text,
    type           text   NOT NULL CHECK (type IN
                       ('covers', 'implements', 'requires', 'wasDerivedFrom', 'blockedBy', 'defers')),
    to_doc         bigint REFERENCES docs(id),
    to_anchor      text,
    to_external    text,
    coverage       text,
    to_rule        bigint REFERENCES rules(id),
    completed_with jsonb,
    CONSTRAINT doc_revision_edges_one_target CHECK (num_nonnulls(to_doc, to_rule, to_external) = 1),
    CONSTRAINT doc_revision_edges_covers_rule CHECK (
        CASE WHEN type = 'covers' THEN to_doc IS NULL AND to_anchor IS NULL ELSE to_rule IS NULL END),
    CONSTRAINT doc_revision_edges_coverage_on_covers CHECK ((type = 'covers') = (coverage IS NOT NULL)),
    CONSTRAINT doc_revision_edges_coverage_level CHECK (coverage IS NULL OR coverage IN ('full', 'partial', 'none')),
    CONSTRAINT doc_revision_edges_defers_from_doc CHECK (type <> 'defers' OR from_anchor IS NULL),
    CONSTRAINT doc_revision_edges_blocked_by_whole_docs
        CHECK (type <> 'blockedBy' OR (from_anchor IS NULL AND to_anchor IS NULL))
);
CREATE UNIQUE INDEX doc_revision_edges_unique ON doc_revision_edges
    (doc_id, coalesce(from_anchor, ''), type, coalesce(to_doc, 0), coalesce(to_rule, 0),
     coalesce(to_anchor, ''), coalesce(to_external, ''));

-- A candidate whose header differs from its document's would be seeded with
-- edges its own header contradicts. Land or discard it, then rerun.
DO $$
DECLARE bad text;
BEGIN
  SELECT string_agg(r.doc_id::text, ', ' ORDER BY r.doc_id) INTO bad
    FROM doc_revisions r JOIN docs d ON d.id = r.doc_id
   WHERE (CASE WHEN left(r.body, 4) = E'---\n' AND position(E'\n---\n' IN substr(r.body, 4)) > 0
               THEN substr(r.body, 1, 3 + position(E'\n---\n' IN substr(r.body, 4)) + 4) ELSE '' END)
      <> (CASE WHEN left(d.body, 4) = E'---\n' AND position(E'\n---\n' IN substr(d.body, 4)) > 0
               THEN substr(d.body, 1, 3 + position(E'\n---\n' IN substr(d.body, 4)) + 4) ELSE '' END);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'open revisions of docs % have a header that differs from the document''s; land or discard them, then rerun', bad;
  END IF;
END $$;

INSERT INTO doc_revision_edges
    (doc_id, from_anchor, type, to_doc, to_anchor, to_external, coverage, to_rule, completed_with)
SELECT e.from_doc, e.from_anchor, e.type, e.to_doc, e.to_anchor, e.to_external, e.coverage, e.to_rule,
       (SELECT jsonb_agg(CASE WHEN w.to_doc IS NOT NULL THEN jsonb_build_object('to_doc', w.to_doc)
                              ELSE jsonb_build_object('to_external', w.to_external) END
                         ORDER BY w.position)
          FROM doc_coverage_completed_with w WHERE w.edge_id = e.id)
  FROM doc_edges e JOIN doc_revisions r ON r.doc_id = e.from_doc;

COMMIT;
