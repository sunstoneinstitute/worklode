-- Reverse 0078. Existing stale and withdrawn documents are retained as
-- superseded before the narrower status constraint is restored.
BEGIN;

UPDATE docs
   SET status = 'superseded'
 WHERE status IN ('stale', 'withdrawn');

ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'superseded'));

ALTER TABLE projects DROP COLUMN doc_staleness_days;
ALTER TABLE tasks DROP COLUMN about_anchor;

COMMIT;
