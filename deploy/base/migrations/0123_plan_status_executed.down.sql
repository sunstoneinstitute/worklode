-- Restore plan status spent (WL-REQ-1664).
BEGIN;

ALTER TABLE docs DROP CONSTRAINT docs_status_check;
UPDATE docs SET status = 'spent' WHERE status = 'executed';
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn', 'spent'));

COMMIT;
