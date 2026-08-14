-- Narrow the task kind enum to the six kinds of spec 025 §10, every one of
-- which names a nature of work. Container-ness is inferred from child_of edges
-- (004 §6.1), so no kind stands for it. Ships with the validKinds and
-- wlc:TaskKind edits in the same commit, which TestTaskKindsAgreeAcrossSources
-- holds together.
--
-- No rows are rewritten: a row outside the six makes the ALTER fail loudly
-- rather than leave an unrepresentable task behind. Decompose stopped writing a
-- structural kind in this same change, and an operator with legacy rows
-- retargets them by hand (their child_of edges already carry the container
-- role).
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','review','spike'));
