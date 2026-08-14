-- Task kinds (docs/specs/025-documents-in-the-backbone.md §8): widen
-- the kind enum by 'review' and 'spike', the two kinds spec 006's wlc:TaskKind
-- has always carried and the database never accepted. No rows change; 'epic'
-- (spec 018) stays. After this the CHECK and wlc:TaskKind hold the same seven.

ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','epic','review','spike'));
