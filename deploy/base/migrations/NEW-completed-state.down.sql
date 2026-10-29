DROP INDEX tasks_one_active_rally;
ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'merged' WHERE state = 'completed';
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN
    ('draft','ready','in_progress','in_review','merged',
     'deployed_dev','deployed_prod','released','abandoned'));
CREATE UNIQUE INDEX tasks_one_active_rally ON tasks (project_id)
    WHERE kind = 'rally'
      AND state NOT IN ('draft','merged','deployed_dev','deployed_prod','released','abandoned')
      AND deleted_at IS NULL;
