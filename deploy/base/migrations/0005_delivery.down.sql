DROP TABLE release_frontiers;
DROP TABLE env_deploys;
DROP TABLE deploy_shas;
DROP TABLE main_commits;
DROP TABLE task_commits;

ALTER TABLE project_repos DROP COLUMN done_state;

ALTER TABLE projects ADD COLUMN deploy_gated boolean NOT NULL DEFAULT false;

ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'done'
    WHERE state IN ('merged','deployed_dev','deployed_prod','released');
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN
    ('draft','ready','in_progress','in_review','done','abandoned'));
