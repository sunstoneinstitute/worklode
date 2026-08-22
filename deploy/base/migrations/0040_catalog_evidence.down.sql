-- Evidence goes first: its event_id is ON DELETE RESTRICT.
DROP TABLE artifact_evidence;
DROP TABLE artifact_declarations;

-- Re-adding the six-source CHECK validates existing rows, so the revert fails
-- loudly if any catalog-sourced event survives rather than silently orphaning
-- the provenance of an ingested fact (0015 does the same for 'web'). An
-- operator must decide what those events become, then re-run.
ALTER TABLE events DROP CONSTRAINT events_source_check;
ALTER TABLE events ADD CONSTRAINT events_source_check
    CHECK (source IN ('github','flux','watcher','cli','system','web'));
