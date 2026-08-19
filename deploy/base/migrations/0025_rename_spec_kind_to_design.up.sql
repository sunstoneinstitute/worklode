-- Rename the 'spec' task kind to 'design' (025 §10, reversed back to this
-- decision by WL-97/PR #79 after a brief detour). "Spec work" read as
-- narrower than the task actually covers: a design document is still called
-- a spec in docs/specs/, but the task kind and the document kind are two
-- different things, and only the task kind moves. Ships with the validKinds
-- and wlc:TaskKind edits in the same commit, which
-- TestTaskKindsAgreeAcrossSources holds together.
--
-- Existing spec-kind rows are migrated in place: no task becomes
-- unrepresentable, and its history (events, edges) is untouched by a value
-- rename on one column. Order matters on a database that holds spec-kind
-- rows: the constraint has to be gone while the rows are renamed, since the
-- old one rejects 'design' and the new one rejects 'spec'.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;

UPDATE tasks SET kind = 'design' WHERE kind = 'spec';

ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike'));
