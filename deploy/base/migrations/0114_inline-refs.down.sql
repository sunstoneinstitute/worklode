BEGIN;

ALTER TABLE rule_versions DROP COLUMN change;
ALTER TABLE doc_versions  DROP COLUMN change;
DROP TABLE inline_refs;

COMMIT;
