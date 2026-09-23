-- A spent plan is closed either way; withdrawn is the closest status the
-- narrower CHECK allows.
BEGIN;

UPDATE docs SET status = 'withdrawn' WHERE status = 'spent';
ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn'));

ALTER TABLE projects DROP COLUMN settings;

COMMIT;
