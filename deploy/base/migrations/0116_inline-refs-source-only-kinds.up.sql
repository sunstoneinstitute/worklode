-- Ideas, decisions and approvals hold Markdown fields, so they are sources of
-- inline references; they are never targets (WL-RULE-1848, WL-REQ-1852).
BEGIN;

ALTER TABLE inline_refs DROP CONSTRAINT inline_refs_from_kind_check;
ALTER TABLE inline_refs ADD CONSTRAINT inline_refs_from_kind_check
    CHECK (from_kind IN ('doc','rule','task','milestone','deliverable','idea','decision','approval'));

COMMIT;
