
One table holds all three kinds, each with its own nullable cascading FK and a CHECK that exactly one is set.

```sql
CREATE TABLE index_chunks (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_kind   text NOT NULL CHECK (subject_kind IN ('doc', 'task', 'skill')),
    doc_id         bigint REFERENCES docs   (id) ON DELETE CASCADE,
    task_id        text   REFERENCES tasks  (id) ON DELETE CASCADE,
    skill_id       bigint REFERENCES skills (id) ON DELETE CASCADE,
    project        text REFERENCES projects (id) ON DELETE RESTRICT,  -- null for org-wide skills
    anchor         text NOT NULL DEFAULT '',
    chunk_index    int  NOT NULL,
    context_header text NOT NULL DEFAULT '',
    chunk_text     text NOT NULL,
    content_hash   text NOT NULL,           -- hash of the subject's source text, computed in SQL
    embedding      vector(768),             -- nullable: cleared on provider change, absent with no provider
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', context_header), 'A') ||
        setweight(to_tsvector('simple', chunk_text), 'B')
    ) STORED,
    indexed_at     timestamptz NOT NULL,
    CONSTRAINT index_chunks_one_subject CHECK (num_nonnulls(doc_id, task_id, skill_id) = 1),
    CONSTRAINT index_chunks_kind_matches_subject CHECK (
        (subject_kind = 'doc'   AND doc_id   IS NOT NULL) OR
        (subject_kind = 'task'  AND task_id  IS NOT NULL) OR
        (subject_kind = 'skill' AND skill_id IS NOT NULL))
);
CREATE UNIQUE INDEX index_chunks_doc   ON index_chunks (doc_id, anchor, chunk_index) WHERE doc_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_task  ON index_chunks (task_id, chunk_index) WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_skill ON index_chunks (skill_id, chunk_index) WHERE skill_id IS NOT NULL;
CREATE INDEX index_chunks_kind_project ON index_chunks (subject_kind, project);
CREATE INDEX index_chunks_embedding ON index_chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX index_chunks_tsv ON index_chunks USING gin (tsv);
```

The `tsv` column is generated, so it cannot drift from `chunk_text`; the two-argument `to_tsvector(regconfig, text)` is IMMUTABLE and the one-argument form must not be used. `embedding_config` is a single row holding the provider id the stored vectors belong to.

