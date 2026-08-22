-- Drops the vocabulary constraint. The up migration's fold of non-conforming
-- labels into 'member' is not reversed — the original labels live in the
-- crew events, not here.
ALTER TABLE project_participants DROP CONSTRAINT project_participants_role_check;
