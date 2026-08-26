-- Store-level version history for documents (025 §4.5): every site that
-- bumps docs.version snapshots the current, pre-update row here first, so
-- reading an old version no longer needs the overwrite to not have happened.
CREATE TABLE doc_versions (
    doc_id     bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    version    integer NOT NULL,
    body       text NOT NULL,
    title      text NOT NULL,
    issued     date,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (doc_id, version)
);
