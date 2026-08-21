-- Skill identity becomes plugin-qualified: <plugin>:<name> (spec 037 §4).
-- One bare name is not org-unique — two plugins under one plugins/*/skills/*
-- source legitimately ship the same skill name, and skills_name_unique made
-- the second one lose arbitrarily.
--
-- The backfill is a placeholder, not the final answer: a manifest-derived
-- qualifier needs the source tarball, which SQL does not have, so existing
-- rows take their source repo's last path segment and the first sync after
-- deploy corrects them. Backfilling before the constraint is what keeps this
-- runnable on a populated registry.
ALTER TABLE skills ADD COLUMN qualifier text NOT NULL DEFAULT '';

UPDATE skills
SET qualifier = regexp_replace(source_repo, '^.*/', '')
WHERE qualifier = '';

-- A skill with no qualifier has no identity: the qualified name would collapse
-- back to the bare one this migration exists to replace.
ALTER TABLE skills ADD CONSTRAINT skills_qualifier_nonempty CHECK (qualifier <> '');

ALTER TABLE skills DROP CONSTRAINT skills_name_unique;
ALTER TABLE skills ADD CONSTRAINT skills_qualified_name_unique UNIQUE (qualifier, name);
