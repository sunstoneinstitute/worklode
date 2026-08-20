-- Tighten the docs number invariant (docs/specs/025-documents-in-the-backbone.md §14.3).
--
-- Migration 0027 shipped `CHECK (kind = 'plan' OR number IS NOT NULL)`, which
-- says only half of what 025 §9/§14.3 mean: a spec or ADR must carry a corpus
-- number, and a plan must not carry one. The old form accepted a plan row with
-- a number; only store.CreateDoc rejected it. The biconditional says the whole
-- invariant in one line, so the schema no longer depends on the layer above it.
--
-- docs_project_kind_number is partial on `number IS NOT NULL`, so no index
-- changes. Renaming the constraint off Postgres's auto-generated `docs_check`
-- also means the violation names itself in the error text.

ALTER TABLE docs DROP CONSTRAINT docs_check;

ALTER TABLE docs ADD CONSTRAINT docs_number_matches_kind
    CHECK ((kind = 'plan') = (number IS NULL));
