-- Add the 'rally' task kind (steers `lode work next`): a hand-assembled
-- goal that carries no work of its own — its blocks edges name the tasks to
-- finish now. At most one is open per project, enforced by the partial
-- unique index below over the live closed states (0005_delivery).

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision','rally'));

CREATE UNIQUE INDEX tasks_one_open_rally ON tasks (project_id)
    WHERE kind = 'rally'
      AND state NOT IN ('merged','deployed_dev','deployed_prod','released','abandoned')
      AND deleted_at IS NULL;
