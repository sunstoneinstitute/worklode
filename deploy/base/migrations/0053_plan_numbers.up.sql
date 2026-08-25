-- Plans join the corpus numbering (spec 029 §4, amending 025 §14.3).
--
-- 025 §14.3 gave plans no number and no shorthand: their handle was their
-- repo-relative path. That made a plan the one document kind a person could
-- not cite, and the one row a listing could not render as <KEY>-<KIND>-<N>.
-- Every kind now draws a number from its project's sequence, so WL-PLAN-7
-- reads exactly like WL-SPEC-29 and WL-ADR-43.
--
-- Flat, not the per-parent-spec pair 029 §4 first specified: a plan's number
-- is one per-project sequence like every other kind's. A plan is identified by
-- what it is, not by what it covers -- coverage already has an edge, and
-- binding identity to it would renumber a plan whenever its covers set moved.

-- The biconditional 0037 shipped -- (kind = 'plan') = (number IS NULL) -- said
-- a plan must NOT carry a number, so it has to go before the backfill can give
-- one to a plan, and the NOT NULL below can only follow once every row has one.
ALTER TABLE docs DROP CONSTRAINT docs_number_matches_kind;

-- Backfill in corpus order -- the slug's date prefix, then the slug -- which is
-- the order internal/designdoc's loadPlans already walks a corpus in, so a
-- plan's number matches the position a reader of the directory would give it.
-- Numbering is per project: two projects' plan 1 are different documents, the
-- same way their spec 1 are.
WITH numbered AS (
    SELECT id, row_number() OVER (PARTITION BY project_id ORDER BY slug, id) AS n
      FROM docs
     WHERE kind = 'plan'
)
UPDATE docs d SET number = numbered.n
  FROM numbered WHERE numbered.id = d.id;

-- Both halves of the old rule are now the same rule: every document carries a
-- number, whatever its kind.
ALTER TABLE docs ALTER COLUMN number SET NOT NULL;

-- docs_project_kind_number was partial on `number IS NOT NULL AND deleted_at
-- IS NULL` (0034). Only the first half goes: with the column NOT NULL that
-- predicate is always true. The `deleted_at IS NULL` half stays, and 0034 says
-- why -- a soft-deleted document must release its number, or re-creating one
-- collides with a row the operator cannot see. Dropping it is what makes a
-- project's plan numbers unique the way its spec numbers already are.
DROP INDEX docs_project_kind_number;
CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, number) WHERE deleted_at IS NULL;

-- PLAN joins the per-project ordinal counters (029 §4). Specs and ADRs still
-- take an author-supplied number -- the filename carries their identity, and
-- moving them onto counters is the wider cutover 029 §4's plan series owns.
-- A plan has no filename number to inherit, so the server allocates it, and
-- the counter starts past whatever the backfill above already used.
ALTER TABLE project_entity_seq DROP CONSTRAINT project_entity_seq_kind_check;
ALTER TABLE project_entity_seq ADD CONSTRAINT project_entity_seq_kind_check
    CHECK (kind IN ('DEL', 'PLAN'));

INSERT INTO project_entity_seq (project_id, kind, next)
SELECT project_id, 'PLAN', max(number) + 1
  FROM docs WHERE kind = 'plan'
 GROUP BY project_id;
