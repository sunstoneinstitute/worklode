-- Fails while any rule is a capability. Reclassify those rows first.
BEGIN;

ALTER TABLE rules DROP CONSTRAINT rules_kind_check;
ALTER TABLE rules ADD CONSTRAINT rules_kind_check
    CHECK (kind IN ('requirement', 'catalogue', 'invariant', 'definition', 'principle', 'informative'));

COMMIT;
