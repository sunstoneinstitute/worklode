-- Postgres baseline (spec 004 schema). All FKs ON DELETE RESTRICT unless noted.

CREATE TABLE events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source      text NOT NULL CHECK (source IN ('github','flux','watcher','cli','system')),
    external_id text NOT NULL,
    type        text NOT NULL,
    payload     jsonb,
    received_at timestamptz NOT NULL,
    UNIQUE (source, external_id)
);

CREATE TABLE actors (
    id           text PRIMARY KEY,
    kind         text NOT NULL CHECK (kind IN ('human', 'agent', 'service')),
    display_name text,
    admin        boolean NOT NULL DEFAULT false
);

CREATE TABLE tokens (
    token_hash  text PRIMARY KEY,
    actor_id    text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    description text,
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz,
    revoked_at  timestamptz
);

CREATE TABLE github_user_tokens (
    actor_id   text PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    ciphertext bytea NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE projects (
    id           text PRIMARY KEY,
    name         text NOT NULL,
    deploy_gated boolean NOT NULL DEFAULT false
);

CREATE TABLE project_repos (
    project_id text NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    repo       text NOT NULL UNIQUE,
    PRIMARY KEY (project_id, repo)
);

CREATE TABLE tasks (
    id         text PRIMARY KEY,                    -- WL-<n>
    project_id text NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    title      text NOT NULL,
    body       text,
    priority   text NOT NULL CHECK (priority IN ('critical','high','medium','low')),
    kind       text NOT NULL CHECK (kind IN ('feature','bug','chore','spec')),
    state      text NOT NULL CHECK (state IN
                 ('draft','ready','in_progress','in_review','done','abandoned')),
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

-- Single-row counter for the global WL-<n> task id sequence.
CREATE TABLE task_seq (id integer PRIMARY KEY CHECK (id = 1), next bigint NOT NULL);
INSERT INTO task_seq (id, next) VALUES (1, 1);

CREATE TABLE task_edges (
    from_task  text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    to_task    text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    type       text NOT NULL CHECK (type IN ('child_of','blocks')),
    created_at timestamptz NOT NULL,
    UNIQUE (from_task, to_task, type)
);

CREATE TABLE leases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text NOT NULL REFERENCES tasks(id)  ON DELETE RESTRICT,
    actor_id    text NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
    worktree    text NOT NULL,
    acquired_at timestamptz NOT NULL,
    renewed_at  timestamptz,
    expires_at  timestamptz NOT NULL,
    released_at timestamptz
);
-- At most one active (unreleased) lease per task, and per worktree.
CREATE UNIQUE INDEX leases_active ON leases (task_id) WHERE released_at IS NULL;
CREATE UNIQUE INDEX leases_active_worktree ON leases (worktree) WHERE released_at IS NULL;

CREATE TABLE issues (
    repo                text NOT NULL,
    number              integer NOT NULL,
    title               text,
    state               text,
    triage_state        text NOT NULL DEFAULT 'new' CHECK (triage_state IN ('new', 'promoted', 'dismissed')),
    task_id             text REFERENCES tasks (id) ON DELETE RESTRICT,
    applies_to_versions text,
    url                 text,
    PRIMARY KEY (repo, number)
);

CREATE TABLE pull_requests (
    repo      text NOT NULL,
    number    integer NOT NULL,
    title     text,
    state     text NOT NULL CHECK (state IN ('open', 'merged', 'closed')),
    task_id   text REFERENCES tasks (id) ON DELETE RESTRICT,
    head_ref  text,
    head_sha  text,
    merge_sha text,
    url       text,
    opened_at timestamptz,
    merged_at timestamptz,
    PRIMARY KEY (repo, number)
);

CREATE TABLE ci_runs (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo         text NOT NULL,
    head_sha     text NOT NULL,
    workflow     text NOT NULL,
    status       text,
    conclusion   text,
    url          text,
    started_at   timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (repo, head_sha, workflow, started_at)
);

CREATE TABLE reviews (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo         text NOT NULL,
    pr_number    integer NOT NULL,
    reviewer     text NOT NULL,
    state        text NOT NULL CHECK (state IN ('approved', 'changes_requested', 'commented')),
    submitted_at timestamptz NOT NULL,
    UNIQUE (repo, pr_number, reviewer, submitted_at)
);

CREATE TABLE artifacts (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind       text NOT NULL CHECK (kind IN ('docker_image', 'pypi', 'git_tag', 'binary')),
    name       text NOT NULL,
    version    text NOT NULL,
    digest     text,
    repo       text,
    source_sha text,
    built_at   timestamptz,
    UNIQUE (kind, name, version)
);

CREATE TABLE deployments (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    artifact_id bigint REFERENCES artifacts (id) ON DELETE RESTRICT,
    environment text NOT NULL,
    target_kind text NOT NULL CHECK (target_kind IN ('flux_kustomization', 'pypi', 'manual')),
    target_name text NOT NULL,
    status      text NOT NULL CHECK (status IN ('pending', 'reconciling', 'deployed', 'failed')),
    first_seen  timestamptz NOT NULL,
    last_update timestamptz NOT NULL,
    UNIQUE (environment, target_kind, target_name)
);

CREATE TABLE runtime_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    cluster     text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('crashloop', 'oom', 'flux_failure', 'flux_recovery')),
    workload    text,
    image       text,
    artifact_id bigint REFERENCES artifacts (id) ON DELETE RESTRICT,
    message     text,
    occurred_at timestamptz NOT NULL
);

CREATE TABLE state_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind text NOT NULL,
    entity_id   text NOT NULL,
    change      jsonb NOT NULL,
    event_id    bigint NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    at          timestamptz NOT NULL
);
