-- Rename plan status spent to executed (WL-REQ-1664).
BEGIN;

ALTER TABLE docs DROP CONSTRAINT docs_status_check;
UPDATE docs SET status = 'executed' WHERE status = 'spent';
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn', 'executed'));

COMMIT;
