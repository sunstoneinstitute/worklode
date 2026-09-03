-- Corpus index: one chunk table over docs, tasks and skills, carrying both
-- retrieval arms' inputs (spec 040 §5). Replaces skill_embeddings (0007).
--
-- Two deviations from 040 §5's printed DDL, both forced:
--   * embedding is NULLABLE. §5 shows NOT NULL, but §8 invalidates a provider
--     change by nulling the column and §11 requires a no-provider instance to
--     write chunk rows with no vectors at all. §8/§11 win.
--   * doc_id is bigint REFERENCES docs(id). §5 writes "text REFERENCES
--     docs(doc_id)"; there is no such column — 0027 keys docs on a bigint id.

CREATE TABLE index_chunks (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject_kind text NOT NULL CHECK (subject_kind IN ('doc', 'task', 'skill')),

    -- Exactly one is set; each cascades, so deleting a subject drops its
    -- chunks and the index needs no tombstones.
    doc_id       bigint REFERENCES docs   (id) ON DELETE CASCADE,
    task_id      text   REFERENCES tasks  (id) ON DELETE CASCADE,
    skill_id     bigint REFERENCES skills (id) ON DELETE CASCADE,

    -- Denormalised so a project-scoped search (019) is one predicate rather
    -- than three joins. Null for skills: the registry is org-wide (016).
    project      text REFERENCES projects (id) ON DELETE RESTRICT,

    -- Frozen section anchor for docs (025 §3.2); '' for everything else.
    anchor       text NOT NULL DEFAULT '',
    chunk_index  int  NOT NULL,

    -- The indexed text itself. Stored because the lexical arm (§6.2) matches
    -- against it, the API excerpts from it, and a re-embed that cannot be
    -- reproduced cannot be verified.
    context_header text NOT NULL DEFAULT '',   -- §4.3
    chunk_text     text NOT NULL,

    -- Hash of the subject's indexed text, identical across all of one
    -- subject's chunks. The convergence loop (§7) compares this against the
    -- live subject; it is the whole freshness mechanism.
    content_hash text NOT NULL,

    -- Fixed width (§2.2): a wrong-shaped vector is refused at INSERT, and the
    -- typmod is what makes the HNSW index below legal. Nullable per §8/§11.
    embedding    vector(768),

    -- The lexical arm. 'simple', not 'english' — see §6.2; the choice is
    -- load-bearing, not stylistic. Generated rather than trigger-maintained so
    -- it cannot drift from chunk_text. Both to_tsvector(regconfig, text) and
    -- setweight are IMMUTABLE, which is what makes a generated column legal;
    -- the one-argument to_tsvector(text) is not, and must not be used here.
    tsv tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', context_header), 'A') ||
        setweight(to_tsvector('simple', chunk_text), 'B')
    ) STORED,

    indexed_at   timestamptz NOT NULL,

    CONSTRAINT index_chunks_one_subject
        CHECK (num_nonnulls(doc_id, task_id, skill_id) = 1),
    CONSTRAINT index_chunks_kind_matches_subject CHECK (
        (subject_kind = 'doc'   AND doc_id   IS NOT NULL) OR
        (subject_kind = 'task'  AND task_id  IS NOT NULL) OR
        (subject_kind = 'skill' AND skill_id IS NOT NULL))
);

CREATE UNIQUE INDEX index_chunks_doc   ON index_chunks (doc_id, anchor, chunk_index)
    WHERE doc_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_task  ON index_chunks (task_id, chunk_index)
    WHERE task_id IS NOT NULL;
CREATE UNIQUE INDEX index_chunks_skill ON index_chunks (skill_id, chunk_index)
    WHERE skill_id IS NOT NULL;

CREATE INDEX index_chunks_kind_project ON index_chunks (subject_kind, project);

CREATE INDEX index_chunks_embedding ON index_chunks
    USING hnsw (embedding vector_cosine_ops);
CREATE INDEX index_chunks_tsv ON index_chunks USING gin (tsv);

-- Rows are not carried over: they are 016-width vectors from a possibly
-- different model, so they are comparable with nothing this spec produces,
-- and they carry none of the text the lexical arm needs. The corpus re-embeds
-- on first convergence. embedding_config survives unchanged (040 §5).
DROP TABLE skill_embeddings;
