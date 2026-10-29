-- Add `completed`: work done with no pull request to deliver (WL-REQ-1884).
ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN
    ('draft','ready','in_progress','in_review','merged',
     'deployed_dev','deployed_prod','released','abandoned','completed'));

-- A completed rally steers nothing, like the other deliveredStateSet states.
DROP INDEX tasks_one_active_rally;
CREATE UNIQUE INDEX tasks_one_active_rally ON tasks (project_id)
    WHERE kind = 'rally'
      AND state NOT IN ('draft','merged','deployed_dev','deployed_prod','released','abandoned','completed')
      AND deleted_at IS NULL;
