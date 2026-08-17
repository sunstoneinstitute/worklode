-- The incremental task fetch (GET /api/v1/tasks?updated_since=) is a range
-- scan on updated_at, made on every poll tick by every mirror, and is
-- otherwise a sequential scan of every task in the instance.
CREATE INDEX tasks_updated_at_idx ON tasks (updated_at);
