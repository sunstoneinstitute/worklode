ALTER TABLE projects ADD COLUMN key text;
ALTER TABLE projects ADD COLUMN next_task_num bigint NOT NULL DEFAULT 1;

-- Backfill key + counter from existing task-id prefixes (data-driven).
-- worklode's tasks are WL-1..WL-11, so it becomes key 'WL', next_task_num 12.
UPDATE projects p SET key = s.prefix, next_task_num = s.maxnum + 1
FROM (SELECT project_id,
             split_part(id, '-', 1)               AS prefix,
             max(split_part(id, '-', 2)::bigint)   AS maxnum
      FROM tasks GROUP BY project_id, split_part(id, '-', 1)) s
WHERE p.id = s.project_id;

-- Fallback for projects with no tasks yet (none in any environment today):
-- derive a key from the id. Assumes the id yields a format-valid key.
UPDATE projects
SET key = upper(substr(regexp_replace(id, '[^a-zA-Z0-9]', '', 'g'), 1, 4))
WHERE key IS NULL;

ALTER TABLE projects ALTER COLUMN key SET NOT NULL;
ALTER TABLE projects ADD CONSTRAINT projects_key_unique UNIQUE (key);
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$');

DROP TABLE task_seq;
