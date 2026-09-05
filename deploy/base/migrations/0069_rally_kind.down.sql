-- Reverse 0069: any rally-kind rows go with it, but tasks(id) has several
-- non-CASCADE foreign keys, so referencing rows are cleared first, in an
-- order that stops each from blocking DELETE FROM tasks.
--
-- Every foreign key onto tasks(id) that is not ON DELETE CASCADE (RESTRICT,
-- or a bare REFERENCES, which defaults to NO ACTION -- the same immediate
-- block as RESTRICT here, since none of these are DEFERRABLE):
--   task_edges.from_task, task_edges.to_task  -- RESTRICT (0001_baseline)
--   leases.task_id                            -- RESTRICT (0001_baseline)
--   issues.task_id                            -- RESTRICT (0001_baseline)
--   pull_requests.task_id                     -- RESTRICT (0001_baseline)
--   docs.generated_by_task                    -- NO ACTION (0044)
--   doc_notes.task_id                         -- NO ACTION (0065)
--   decisions.task_id                         -- NO ACTION, NOT NULL (0068)
--
-- Drop the index before the rows so its predicate never has to evaluate
-- against a kind the CHECK is about to disallow.

DROP INDEX tasks_one_open_rally;

-- A rally's blocks edges are the expected case, not an edge case.
DELETE FROM task_edges
    WHERE from_task IN (SELECT id FROM tasks WHERE kind = 'rally')
       OR to_task   IN (SELECT id FROM tasks WHERE kind = 'rally');

-- Rally is not yet unclaimable (a later task adds that rule), so a lease on
-- one is possible today.
DELETE FROM leases WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');

-- Reachable via `lode inbox promote --kind rally`. The issue/PR tracking
-- row is not rally's to delete, only its link to the task.
UPDATE issues SET task_id = NULL
    WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');
UPDATE pull_requests SET task_id = NULL
    WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');

-- Reachable through a claimed worktree session -- the same claimability gap
-- as leases above.
UPDATE docs SET generated_by_task = NULL
    WHERE generated_by_task IN (SELECT id FROM tasks WHERE kind = 'rally');
UPDATE doc_notes SET task_id = NULL
    WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');

-- task_id is NOT NULL, so unlike the above there is no null to fall back
-- to; a rally's decisions rows go with it.
DELETE FROM decisions WHERE task_id IN (SELECT id FROM tasks WHERE kind = 'rally');

DELETE FROM tasks WHERE kind = 'rally';

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'));
