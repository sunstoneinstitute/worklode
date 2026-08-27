-- Steering instructions: operator-authored messages queued against a task,
-- delivered to whichever actor next claims that task's lease. Write side
-- stays task-addressed; the claim side (store layer) scopes by joining
-- against the calling actor's currently leased tasks.
CREATE TABLE task_instructions (
    id           bigserial PRIMARY KEY,
    task_id      text NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    body         text NOT NULL,
    created_by   text REFERENCES actors (id),
    created_at   timestamptz NOT NULL,
    delivered_at timestamptz
);

CREATE INDEX task_instructions_pending ON task_instructions (task_id, id)
    WHERE delivered_at IS NULL;
