-- Task due dates (WL-REQ-1796). Closed is per repo (taskClosed), so the
-- partial index excludes only the states closed in every repo: abandoned and
-- the top delivery rank. Past-due queries apply taskClosed on top.
ALTER TABLE tasks ADD COLUMN due date;

CREATE INDEX tasks_due_idx ON tasks (project_id, due)
    WHERE due IS NOT NULL AND state NOT IN ('abandoned', 'deployed_prod', 'released');
