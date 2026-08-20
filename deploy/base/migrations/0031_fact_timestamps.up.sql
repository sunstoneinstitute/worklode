-- The fact's own last-modified time, so an upsert carrying an older payload
-- cannot overwrite newer state (see UpsertPR in internal/store/changes.go).
-- Existing rows stay NULL: unknown sorts as -infinity, so the first event
-- that carries a timestamp wins.

ALTER TABLE pull_requests ADD COLUMN updated_at timestamptz;
ALTER TABLE issues ADD COLUMN updated_at timestamptz;
ALTER TABLE ci_runs ADD COLUMN updated_at timestamptz;
