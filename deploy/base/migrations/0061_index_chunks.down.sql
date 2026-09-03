DROP TABLE index_chunks;

-- Restored as 0007_skills.up.sql defined it. Empty: the vectors 0061 dropped
-- were not carried forward and cannot be reconstructed without re-embedding.
CREATE TABLE skill_embeddings (
    skill_id    bigint NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    chunk_index int NOT NULL,
    embedding   vector NOT NULL,
    PRIMARY KEY (skill_id, chunk_index)
);
