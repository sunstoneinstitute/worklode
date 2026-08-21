-- Per-project quarantine for the backbone→knowledge-graph projector (spec 006
-- §11). The projector tracks one global watermark (graph_projection), so
-- before this table a single project graph-server kept rejecting held that
-- watermark back for every project: nothing was projected anywhere until the
-- bad one was fixed.
--
-- Now RunOnce advances the watermark past a failed project and records the
-- failure here. The row is the only memory that the project still owes a
-- projection once its state_log rows are behind the watermark, so the
-- projector re-attempts it on its own backoff schedule (next_attempt_at)
-- until it succeeds, at which point the row is deleted. A project that is
-- dirty again on its own is attempted regardless of next_attempt_at: new
-- content is exactly the event most likely to clear a content-specific
-- rejection.
CREATE TABLE graph_projection_failures (
    project_id      text        PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    attempts        integer     NOT NULL,
    first_failed_at timestamptz NOT NULL,
    last_failed_at  timestamptz NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    last_error      text        NOT NULL
);
