-- Spec-less plan 2026-08-06-human-assignment: a human owns a task without
-- holding a lease. NULL = unassigned. Partial index backs "my tasks".
ALTER TABLE tasks ADD COLUMN assignee text REFERENCES actors (id);
CREATE INDEX tasks_assignee ON tasks (assignee) WHERE assignee IS NOT NULL;
