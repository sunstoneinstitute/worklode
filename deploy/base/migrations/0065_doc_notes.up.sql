-- WL-SPEC-77: anchored, non-blocking notes. task_id/session_id link the note
-- to what raised it; both nullable — a human at a prompt has neither.
CREATE TABLE doc_notes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_id     bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    anchor     text NOT NULL,
    body       text NOT NULL,
    task_id    text REFERENCES tasks(id),
    session_id text,
    created_by text REFERENCES actors(id),
    created_at timestamptz NOT NULL
);
CREATE INDEX doc_notes_doc_anchor ON doc_notes (doc_id, anchor);

-- WL-SPEC-77: approved text, modified since. Set by the §8.2 patch path,
-- cleared when the original approvers re-approve.
ALTER TABLE doc_sections ADD COLUMN patched boolean NOT NULL DEFAULT false;
