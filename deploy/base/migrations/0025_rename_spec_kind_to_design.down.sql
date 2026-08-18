-- Reverse 0025: restore the 'spec' kind and migrate 'design' rows back to
-- it. Only rows this migration (or work done after it) created as 'design'
-- are affected; the rename is exact in both directions since 'design' was
-- not a legal kind before this migration ran.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','review','spike'));

UPDATE tasks SET kind = 'spec' WHERE kind = 'design';
