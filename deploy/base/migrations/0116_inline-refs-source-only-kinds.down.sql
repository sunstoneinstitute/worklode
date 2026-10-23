BEGIN;

DELETE FROM inline_refs WHERE from_kind IN ('idea','decision','approval');
ALTER TABLE inline_refs DROP CONSTRAINT inline_refs_from_kind_check;
ALTER TABLE inline_refs ADD CONSTRAINT inline_refs_from_kind_check
    CHECK (from_kind IN ('doc','rule','task','milestone','deliverable'));

COMMIT;
