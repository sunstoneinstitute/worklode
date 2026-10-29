-- WL-REQ-1319, WL-REQ-1667: a doc note is a bare thread (review_id NULL)
-- anchored to its section alone. fold_note_id carries each note's id so the
-- message join and the inline_refs rewrite are exact.
BEGIN;

-- A note with no created_by is credited to the worklode system actor.
INSERT INTO actors (id, kind, display_name)
SELECT 'worklode', 'service', 'Worklode'
 WHERE EXISTS (SELECT 1 FROM doc_notes WHERE created_by IS NULL)
ON CONFLICT (id) DO NOTHING;

ALTER TABLE review_threads ADD COLUMN fold_note_id bigint;
-- The CASE keeps the cast off a non-numeric session_id: Postgres does not
-- promise short-circuit evaluation of a WHERE conjunction.
INSERT INTO review_threads
    (doc_id, doc_anchor, raised_by_task, raised_by_session,
     intent, status, author_id, created_at, fold_note_id)
SELECT n.doc_id, n.anchor, n.task_id,
       (SELECT s.id FROM agent_sessions s
         WHERE s.id = CASE WHEN n.session_id ~ '^[0-9]{1,18}$'
                           THEN n.session_id::bigint END),
       'note', 'open', COALESCE(n.created_by, 'worklode'), n.created_at, n.id
  FROM doc_notes n ORDER BY n.id;
-- The note's text is its first message; seq is per thread for a bare note.
INSERT INTO review_messages (thread_id, seq, actor_id, role, body, created_at)
SELECT t.id, 1, t.author_id, 'reviewer', n.body, n.created_at
  FROM review_threads t JOIN doc_notes n ON n.id = t.fold_note_id;

-- A note body's inline refs are held by its doc as note/<id>; the id is now
-- the thread's. Two steps, so no rename collides with a row not yet renamed.
UPDATE inline_refs r SET field = 'fold:' || t.id
  FROM review_threads t
 WHERE t.fold_note_id IS NOT NULL AND r.from_kind = 'doc'
   AND r.from_id = t.doc_id::text AND r.field = 'note/' || t.fold_note_id;
UPDATE inline_refs SET field = 'note/' || substr(field, 6) WHERE field LIKE 'fold:%';

ALTER TABLE review_threads DROP COLUMN fold_note_id;
DROP TABLE doc_notes;

COMMIT;
