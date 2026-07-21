-- v1 schema. All timestamps are TEXT, UTC RFC3339. All FKs ON DELETE RESTRICT.

CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT NOT NULL CHECK (source IN ('github', 'flux', 'watcher', 'cli', 'system')),
    external_id TEXT NOT NULL,
    type        TEXT NOT NULL,
    payload     TEXT,
    received_at TEXT NOT NULL,
    UNIQUE (source, external_id)
);

CREATE TABLE actors (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL CHECK (kind IN ('human', 'agent', 'service')),
    display_name TEXT
);

CREATE TABLE tokens (
    token_hash  TEXT PRIMARY KEY,
    actor_id    TEXT NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    description TEXT,
    created_at  TEXT NOT NULL,
    expires_at  TEXT,
    revoked_at  TEXT
);

CREATE TABLE projects (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    deploy_gated INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE project_repos (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    repo       TEXT NOT NULL UNIQUE,
    PRIMARY KEY (project_id, repo)
);

CREATE TABLE tasks (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    title      TEXT NOT NULL,
    body       TEXT,
    priority   TEXT NOT NULL CHECK (priority IN ('critical', 'high', 'medium', 'low')),
    kind       TEXT NOT NULL CHECK (kind IN ('feature', 'bug', 'chore', 'spec')),
    state      TEXT NOT NULL CHECK (state IN ('draft', 'ready', 'in_progress', 'in_review', 'done', 'abandoned')),
    created_by TEXT REFERENCES actors (id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Single-row counter for the global WL-<n> task id sequence.
CREATE TABLE task_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
);
INSERT INTO task_seq (id, next) VALUES (1, 1);

CREATE TABLE task_edges (
    from_task  TEXT NOT NULL REFERENCES tasks (id) ON DELETE RESTRICT,
    to_task    TEXT NOT NULL REFERENCES tasks (id) ON DELETE RESTRICT,
    type       TEXT NOT NULL CHECK (type IN ('child_of', 'blocks')),
    created_at TEXT NOT NULL,
    UNIQUE (from_task, to_task, type)
);

CREATE TABLE leases (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     TEXT NOT NULL REFERENCES tasks (id) ON DELETE RESTRICT,
    actor_id    TEXT NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    session_id  TEXT,
    acquired_at TEXT NOT NULL,
    renewed_at  TEXT,
    expires_at  TEXT NOT NULL,
    released_at TEXT
);

-- At most one active (unreleased) lease per task.
CREATE UNIQUE INDEX leases_active ON leases (task_id) WHERE released_at IS NULL;

CREATE TABLE issues (
    repo                TEXT NOT NULL,
    number              INTEGER NOT NULL,
    title               TEXT,
    state               TEXT,
    triage_state        TEXT NOT NULL DEFAULT 'new' CHECK (triage_state IN ('new', 'promoted', 'dismissed')),
    task_id             TEXT REFERENCES tasks (id) ON DELETE RESTRICT,
    applies_to_versions TEXT,
    url                 TEXT,
    PRIMARY KEY (repo, number)
);

CREATE TABLE pull_requests (
    repo      TEXT NOT NULL,
    number    INTEGER NOT NULL,
    title     TEXT,
    state     TEXT NOT NULL CHECK (state IN ('open', 'merged', 'closed')),
    task_id   TEXT REFERENCES tasks (id) ON DELETE RESTRICT,
    head_ref  TEXT,
    head_sha  TEXT,
    merge_sha TEXT,
    url       TEXT,
    opened_at TEXT,
    merged_at TEXT,
    PRIMARY KEY (repo, number)
);

CREATE TABLE ci_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    repo         TEXT NOT NULL,
    head_sha     TEXT NOT NULL,
    workflow     TEXT NOT NULL,
    status       TEXT,
    conclusion   TEXT,
    url          TEXT,
    started_at   TEXT NOT NULL,
    completed_at TEXT,
    UNIQUE (repo, head_sha, workflow, started_at)
);

CREATE TABLE reviews (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    repo         TEXT NOT NULL,
    pr_number    INTEGER NOT NULL,
    reviewer     TEXT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('approved', 'changes_requested', 'commented')),
    submitted_at TEXT NOT NULL,
    UNIQUE (repo, pr_number, reviewer, submitted_at)
);

CREATE TABLE artifacts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL CHECK (kind IN ('docker_image', 'pypi', 'git_tag', 'binary')),
    name       TEXT NOT NULL,
    version    TEXT NOT NULL,
    digest     TEXT,
    repo       TEXT,
    source_sha TEXT,
    built_at   TEXT,
    UNIQUE (kind, name, version)
);

CREATE TABLE deployments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id INTEGER REFERENCES artifacts (id) ON DELETE RESTRICT,
    environment TEXT NOT NULL,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('flux_kustomization', 'pypi', 'manual')),
    target_name TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('pending', 'reconciling', 'deployed', 'failed')),
    first_seen  TEXT NOT NULL,
    last_update TEXT NOT NULL,
    UNIQUE (environment, target_kind, target_name)
);

CREATE TABLE runtime_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    cluster     TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('crashloop', 'oom', 'flux_failure', 'flux_recovery')),
    workload    TEXT,
    image       TEXT,
    artifact_id INTEGER REFERENCES artifacts (id) ON DELETE RESTRICT,
    message     TEXT,
    occurred_at TEXT NOT NULL
);

CREATE TABLE state_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_kind TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    change      TEXT NOT NULL,
    event_id    INTEGER NOT NULL REFERENCES events (id) ON DELETE RESTRICT,
    at          TEXT NOT NULL
);
