-- WL-SPEC-66 §3.5: a project has at most one DRAFT rally, the one the
-- Progress page assembles into. 0069 made the ACTIVE rally singular; this
-- does the same for the draft, so "the draft rally" names one row and two
-- Rally clicks racing each other cannot each create their own.

CREATE UNIQUE INDEX tasks_one_draft_rally ON tasks (project_id)
    WHERE kind = 'rally' AND state = 'draft' AND deleted_at IS NULL;
