DROP TABLE deliverables;
DROP TABLE project_entity_seq;

-- Re-adding the five-source CHECK validates existing rows, so the revert fails
-- loudly if any web-sourced event survives rather than silently orphaning the
-- provenance of a cockpit write. An operator must decide what those events
-- become, then re-run.
ALTER TABLE events DROP CONSTRAINT events_source_check;
ALTER TABLE events ADD CONSTRAINT events_source_check
    CHECK (source IN ('github','flux','watcher','cli','system'));
