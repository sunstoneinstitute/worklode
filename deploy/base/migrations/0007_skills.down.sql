ALTER TABLE tasks DROP COLUMN skills;
DROP TABLE skill_embeddings;
ALTER TABLE skills DROP CONSTRAINT skills_latest_version_fk;
DROP TABLE skill_versions;
DROP TABLE skills;
-- The vector extension is left installed: it is not part of the schema this
-- migration created, and CREATE EXTENSION IF NOT EXISTS is idempotent on re-up.
