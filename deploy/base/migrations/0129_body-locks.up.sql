-- WL-REQ-1868: one editor at a time. A lock is held while it has a live
-- session; a second tab is a second session under the same holder.
BEGIN;

CREATE TABLE body_locks (
    kind       text        NOT NULL CHECK (kind IN ('doc', 'rule', 'task')),
    subject_id text        NOT NULL,
    holder     text        NOT NULL,
    taken_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, subject_id)
);

CREATE TABLE body_lock_sessions (
    id            text        PRIMARY KEY,
    kind          text        NOT NULL CHECK (kind IN ('doc', 'rule', 'task')),
    subject_id    text        NOT NULL,
    holder        text        NOT NULL,
    dirty         boolean     NOT NULL DEFAULT false,
    last_activity timestamptz NOT NULL DEFAULT now(),
    checked_in_at timestamptz NOT NULL DEFAULT now(),
    ended_at      timestamptz,
    ended_reason  text        CHECK (ended_reason IN ('released', 'unlocked', 'inactive', 'expired')),
    ended_by      text,
    CHECK ((ended_at IS NULL) = (ended_reason IS NULL))
);

CREATE INDEX body_lock_sessions_live ON body_lock_sessions (kind, subject_id)
    WHERE ended_at IS NULL;

COMMIT;
