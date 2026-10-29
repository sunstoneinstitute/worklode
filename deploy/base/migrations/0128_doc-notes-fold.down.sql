-- Recreates doc_notes (0065) from the bare threads anchored to a section
-- alone. Each note keeps its thread's id, so note/<id> inline refs still
-- match. Replies to a note have no doc_notes column and are dropped.
BEGIN;

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

INSERT INTO doc_notes (id, doc_id, anchor, body, task_id, session_id, created_by, created_at)
OVERRIDING SYSTEM VALUE
SELECT t.id, t.doc_id, t.doc_anchor, m.body, t.raised_by_task, t.raised_by_session,
       t.author_id, t.created_at
  FROM review_threads t JOIN review_messages m ON m.thread_id = t.id AND m.seq = 1
 WHERE t.review_id IS NULL AND t.doc_id IS NOT NULL
   AND t.doc_anchor IS NOT NULL AND t.version IS NULL;
SELECT setval(pg_get_serial_sequence('doc_notes', 'id'), COALESCE(max(id), 0) + 1, false)
  FROM doc_notes;

DELETE FROM review_messages WHERE thread_id IN (SELECT id FROM doc_notes);
DELETE FROM review_threads WHERE id IN (SELECT id FROM doc_notes);

COMMIT;
