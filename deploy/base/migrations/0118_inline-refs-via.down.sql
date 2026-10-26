-- Rows that differ only in via collapse into one before the old key returns.
BEGIN;

DELETE FROM inline_refs a USING inline_refs b
 WHERE a.ctid > b.ctid AND a.from_kind = b.from_kind AND a.from_id = b.from_id AND a.field = b.field
   AND a.from_version IS NOT DISTINCT FROM b.from_version AND a.to_kind = b.to_kind AND a.to_id = b.to_id;
DROP INDEX inline_refs_key;
CREATE UNIQUE INDEX inline_refs_key ON inline_refs
    (from_kind, from_id, field, COALESCE(from_version, -1), to_kind, to_id);
ALTER TABLE inline_refs DROP COLUMN via_doc, DROP COLUMN via_anchor;

COMMIT;
