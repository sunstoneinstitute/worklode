-- Reserve SPEC and ADR as project keys (025 §14.3, amending 004 §2.2).
--
-- They are the <TYPE> token of the <PROJECTKEY>-<TYPE>-<n> document shorthand.
-- Resolution keys the shorthand on the project key alone so it can cross
-- corpora; a project keyed SPEC or ADR would make WL-SPEC-1 readable two ways.

ALTER TABLE projects DROP CONSTRAINT projects_key_format;
ALTER TABLE projects ADD CONSTRAINT projects_key_format
    CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$' AND key NOT IN ('SPEC', 'ADR'));
