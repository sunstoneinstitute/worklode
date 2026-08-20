-- Spec 026 §2.1/§5 makes plan coverage three-valued: a `covers` entry carries
-- `coverage: full | partial | none`, and a `partial` may name the plans that
-- jointly close the section under `fullCoverageWith`. doc_edges could express
-- neither, so rebuildEdges dropped both and NeedsPlanning approximated
-- coverage as two-valued (WL-141).
--
-- doc_edges is itself the reified wl:Coverage node of 026 §6 -- it names both
-- ends and carries a surrogate id -- so the level is a column on it and
-- wl:completedWith is a child table keyed by that id.

ALTER TABLE doc_edges ADD COLUMN coverage text;

-- Rows written before this column carry no recoverable level. The two-valued
-- query counted every one of them as discharging its section, so 'full' is the
-- backfill that preserves the answer; the authored level returns the next time
-- the document's body is written and its edges rebuild.
UPDATE doc_edges SET coverage = 'full' WHERE type = 'covers';

ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_coverage_level
    CHECK (coverage IS NULL OR coverage IN ('full','partial','none'));
-- Only a covers edge carries a level, and every covers edge carries one.
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_coverage_on_covers
    CHECK ((type = 'covers') = (coverage IS NOT NULL));

-- wl:completedWith (026 §6): the plans a `partial` entry names as jointly
-- finishing the section. Ordered, because frontmatter order is the authored
-- order. to_doc/to_external mirror doc_edges -- a reference this project
-- cannot resolve is kept verbatim and, being unresolvable, closes nothing
-- (026 §2.1: fullCoverageWith is checked, never taken on trust).
CREATE TABLE doc_coverage_completed_with (
    edge_id     bigint NOT NULL REFERENCES doc_edges(id) ON DELETE CASCADE,
    position    integer NOT NULL,
    to_doc      bigint REFERENCES docs(id),
    to_external text,
    PRIMARY KEY (edge_id, position),
    CHECK ((to_doc IS NULL) <> (to_external IS NULL))
);
