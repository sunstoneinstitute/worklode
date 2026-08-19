-- Reverse 0025: restore the 'spec' kind and migrate 'design' rows back to
-- it. Only rows this migration (or work done after it) created as 'design'
-- are affected; the rename is exact in both directions since 'design' was
-- not a legal kind before this migration ran. Same ordering rule as the up
-- migration: rename the rows while no kind constraint is in force.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;

UPDATE tasks SET kind = 'spec' WHERE kind = 'design';

ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','review','spike'));
