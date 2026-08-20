DROP INDEX events_unapplied;
ALTER TABLE events DROP COLUMN applied_at;
ALTER TABLE project_repos DROP COLUMN mapped_at;
