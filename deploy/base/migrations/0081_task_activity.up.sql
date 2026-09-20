-- Spec 071 §2: a per-task activity log fed by Claude Code's OTel log events
-- (tool_result, and similar). Kept separate from events: events is
-- append-only provenance that is never purged, but these arrive hundreds
-- per session and are worthless once the task closes (012 §3's "a heartbeat
-- is not an event worth keeping" reasoning). A later task adds the purge.
CREATE TABLE task_activity (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_id    text NOT NULL,
    agent       text NOT NULL,
    session_id  text NOT NULL,
    at          timestamptz NOT NULL,
    event       text NOT NULL,          -- claude_code.tool_result, ...
    attrs       jsonb NOT NULL DEFAULT '{}',
    recorded_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX task_activity_task_id_idx ON task_activity (task_id, id);
CREATE INDEX task_activity_at_idx ON task_activity (at);
