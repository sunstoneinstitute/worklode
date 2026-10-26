BEGIN;

ALTER TABLE task_governed_by DROP COLUMN classification_id;
ALTER TABLE doc_edges        DROP COLUMN classification_id;
ALTER TABLE rule_edges       DROP COLUMN classification_id;
ALTER TABLE task_edges       DROP COLUMN classification_id;
DROP TABLE ref_classifications;

COMMIT;
