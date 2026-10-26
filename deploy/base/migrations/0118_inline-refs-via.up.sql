-- A section ref keeps the resolution it got when written: via_doc is the doc a
-- rule was reached through, via_anchor the section anchor as written
-- (WL-REQ-1852, WL-RULE-1849).
BEGIN;

ALTER TABLE inline_refs ADD COLUMN via_doc text, ADD COLUMN via_anchor text;
DROP INDEX inline_refs_key;
CREATE UNIQUE INDEX inline_refs_key ON inline_refs
    (from_kind, from_id, field, COALESCE(from_version, -1), to_kind, to_id,
     COALESCE(via_doc, ''), COALESCE(via_anchor, ''));

COMMIT;
