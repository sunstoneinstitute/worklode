-- Rule kind capability (WL-REQ-1642).
BEGIN;

ALTER TABLE rules DROP CONSTRAINT rules_kind_check;
ALTER TABLE rules ADD CONSTRAINT rules_kind_check
    CHECK (kind IN ('requirement', 'catalogue', 'invariant', 'definition', 'principle', 'capability', 'informative'));

COMMIT;
