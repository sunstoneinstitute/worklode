-- Reverse 0069: a down is destructive by definition, so any rally-kind rows
-- go with it. A rally's whole purpose is to carry blocks edges, so
-- task_edges rows are the expected case, not an edge case; leases are
-- possible too since rally is not yet unclaimable (that rule lands in a
-- later task). Both from_task/to_task and leases.task_id are ON DELETE
-- RESTRICT (0001_baseline), so they must go before the DELETE FROM tasks or
-- it fails with a foreign-key violation. issues.task_id and
-- pull_requests.task_id are the same RESTRICT shape (0001_baseline) and are
-- reachable from `lode inbox promote --kind rally`, so they are nulled
-- rather than deleted — the issue/PR row itself is not rally's to remove,
-- only its link to the task going away is. These four are every RESTRICT
-- foreign key onto tasks(id) (grep the migrations for
-- "REFERENCES tasks.*RESTRICT"); everything else referencing tasks(id) is
-- ON DELETE CASCADE or, for decisions.task_id, provably decision-only
-- (kind is fixed at creation — internal/api/tasks.go).
--
-- Drop the index before the rows so the index's own predicate never has to
-- evaluate against a kind the CHECK is about to disallow.

DROP INDEX tasks_one_open_rally;

UPDATE pull_requests SET task_id = NULL
    WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');
UPDATE issues SET task_id = NULL
    WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');
DELETE FROM leases WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');
DELETE FROM task_edges
    WHERE from_task IN (SELECT id FROM tasks WHERE kind = 'rally')
       OR to_task   IN (SELECT id FROM tasks WHERE kind = 'rally');
DELETE FROM tasks WHERE kind = 'rally';

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'));
