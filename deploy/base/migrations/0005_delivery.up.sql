-- Delivery lifecycle (docs/specs/004-execution-backbone.md):
-- rename done -> merged, add delivery states, fact tables, per-repo done_state.

ALTER TABLE tasks DROP CONSTRAINT tasks_state_check;
UPDATE tasks SET state = 'merged' WHERE state = 'done';
ALTER TABLE tasks ADD CONSTRAINT tasks_state_check CHECK (state IN
    ('draft','ready','in_progress','in_review','merged',
     'deployed_dev','deployed_prod','released','abandoned'));

ALTER TABLE projects DROP COLUMN deploy_gated;

ALTER TABLE project_repos ADD COLUMN done_state text NOT NULL DEFAULT 'merged'
    CHECK (done_state IN ('merged','deployed_prod','released'));

-- Commits attributed to a task (from task-branch pushes, PRs, merge-commit
-- messages, or WL-Task markers on main).
CREATE TABLE task_commits (
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    repo    text NOT NULL,
    sha     text NOT NULL,
    source  text NOT NULL CHECK (source IN ('branch_push','pr','merge_message','marker')),
    seen_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, repo, sha)
);
CREATE INDEX task_commits_repo_sha ON task_commits (repo, sha);

-- Every commit pushed to a repo's default branch, in push order. The id is
-- the "seq": inclusion checks are integer comparisons per repo.
CREATE TABLE main_commits (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo      text NOT NULL,
    sha       text NOT NULL,
    pushed_at timestamptz NOT NULL,
    UNIQUE (repo, sha)
);

-- Maps a deploy-branch (last-deploy/*) commit back to the main commit its
-- main-sha: trailer names.
CREATE TABLE deploy_shas (
    repo    text NOT NULL,
    sha     text NOT NULL,
    main_id bigint NOT NULL REFERENCES main_commits(id) ON DELETE CASCADE,
    PRIMARY KEY (repo, sha)
);

-- Per-environment deployed frontier: forward-only watermarks per signal.
-- flux_seen latches on the first correlated Flux event; before that the
-- GitHub signal alone confirms (bootstrap fallback per spec).
CREATE TABLE env_deploys (
    repo         text NOT NULL,
    environment  text NOT NULL CHECK (environment IN ('dev','prod')),
    gh_main_id   bigint REFERENCES main_commits(id) ON DELETE SET NULL,
    flux_main_id bigint REFERENCES main_commits(id) ON DELETE SET NULL,
    flux_seen    boolean NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (repo, environment)
);

-- Latest main commit covered by each published release.
CREATE TABLE release_frontiers (
    repo         text NOT NULL,
    tag          text NOT NULL,
    main_id      bigint NOT NULL REFERENCES main_commits(id) ON DELETE CASCADE,
    published_at timestamptz NOT NULL,
    PRIMARY KEY (repo, tag)
);
