-- Spec 025 §8.1, §8.6 and §8.7: retain section-scoped escalation context,
-- permit stale and withdrawn documents, and configure grooming per project.
BEGIN;

ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn'));

ALTER TABLE tasks ADD COLUMN about_anchor text;

ALTER TABLE projects ADD COLUMN doc_staleness_days integer
    CHECK (doc_staleness_days > 0);

COMMIT;
