-- Reverting drops the qualifier, so two plugins' same-named skills collide
-- again. Keep the lowest id per name and drop the rest; the next sync re-adds
-- whichever one the (restored) bare-name constraint lets back in.
-- skill_versions.skill_id is ON DELETE RESTRICT, so versions go first, and
-- latest_version_id is cleared before that so it does not dangle.
CREATE TEMP TABLE skill_qualifier_losers ON COMMIT DROP AS
SELECT a.id FROM skills a JOIN skills b ON a.name = b.name AND a.id > b.id;

UPDATE skills SET latest_version_id = NULL
WHERE id IN (SELECT id FROM skill_qualifier_losers);

DELETE FROM skill_versions WHERE skill_id IN (SELECT id FROM skill_qualifier_losers);
DELETE FROM skills WHERE id IN (SELECT id FROM skill_qualifier_losers);

ALTER TABLE skills DROP CONSTRAINT skills_qualifier_nonempty;
ALTER TABLE skills DROP CONSTRAINT skills_qualified_name_unique;
ALTER TABLE skills ADD CONSTRAINT skills_name_unique UNIQUE (name);
ALTER TABLE skills DROP COLUMN qualifier;
