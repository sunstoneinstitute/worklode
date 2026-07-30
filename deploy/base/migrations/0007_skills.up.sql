-- Org-wide agent skills: registry synced from git source repos, chunked
-- pgvector embeddings for recommendation, and per-task skill pins.
-- See docs/specs/016-org-wide-skills.md.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE skills (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name              text NOT NULL,
    description       text NOT NULL,
    source_repo       text NOT NULL,
    source_path       text NOT NULL,
    latest_version_id bigint,
    deleted_at        timestamptz,
    CONSTRAINT skills_name_unique UNIQUE (name)
);

CREATE TABLE skill_versions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    skill_id     bigint NOT NULL REFERENCES skills(id) ON DELETE RESTRICT,
    git_commit   text NOT NULL,
    content_hash text NOT NULL,
    frontmatter  jsonb NOT NULL,
    skill_md     text NOT NULL,
    archive      bytea NOT NULL,
    created_at   timestamptz NOT NULL,
    CONSTRAINT skill_versions_hash_unique UNIQUE (skill_id, content_hash)
);

ALTER TABLE skills
    ADD CONSTRAINT skills_latest_version_fk
    FOREIGN KEY (latest_version_id) REFERENCES skill_versions(id) ON DELETE SET NULL;

-- Latest version only; empty when no embedding provider is configured.
--   embedding: no dimension typmod (unindexed exact scan). Mixed dimensions in
--   this table make cosine queries error, so a model change must re-embed all
--   rows in one transaction.
CREATE TABLE skill_embeddings (
    skill_id    bigint NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    chunk_index int NOT NULL,
    embedding   vector NOT NULL,
    PRIMARY KEY (skill_id, chunk_index)
);

-- The embedding space the stored vectors belong to. Vectors from different
-- providers/models are not comparable, so a change invalidates all of them.
CREATE TABLE embedding_config (
    singleton   boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    provider_id text NOT NULL
);

-- Task pins: skill names the task author wants injected into the brief.
ALTER TABLE tasks ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';
