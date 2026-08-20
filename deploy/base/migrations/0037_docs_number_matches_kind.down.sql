-- Restore migration 0027's looser constraint, under the auto-generated name it
-- carried there so a re-run of 0037 finds it again.

ALTER TABLE docs DROP CONSTRAINT docs_number_matches_kind;

ALTER TABLE docs ADD CONSTRAINT docs_check
    CHECK (kind = 'plan' OR number IS NOT NULL);
