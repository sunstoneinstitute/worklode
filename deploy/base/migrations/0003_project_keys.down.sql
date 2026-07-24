CREATE TABLE task_seq (id integer PRIMARY KEY CHECK (id = 1), next bigint NOT NULL);
INSERT INTO task_seq (id, next)
VALUES (1, COALESCE((SELECT max(next_task_num) FROM projects), 1));

ALTER TABLE projects DROP CONSTRAINT projects_key_format;
ALTER TABLE projects DROP CONSTRAINT projects_key_unique;
ALTER TABLE projects DROP COLUMN next_task_num;
ALTER TABLE projects DROP COLUMN key;
