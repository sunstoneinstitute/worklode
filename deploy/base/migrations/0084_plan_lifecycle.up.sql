-- Plan lifecycle (docs/specs2/12-spec-refactoring-design-tree.md S5, S19):
-- a plan whose every minted task has closed is spent, and a project may
-- override the server's plan token budget. settings is a small JSON object
-- behind a server-side key allowlist (internal/store/projectsettings.go).
BEGIN;

ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn', 'spent'));

ALTER TABLE projects ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';

COMMIT;
