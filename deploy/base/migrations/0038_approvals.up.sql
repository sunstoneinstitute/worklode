-- The PR author's GitHub login, filled by the pull_request ingest from
-- payload user.login. Nullable: rows ingested before this column stay NULL,
-- and the self-approval check degrades to "cannot refuse" on NULL.
ALTER TABLE pull_requests ADD COLUMN author text;

-- Spec 029 §7.1: one table, every approval. A missing approval is a visible
-- 'awaiting' row. entity_kind is unconstrained text: 'pr' is the only value
-- this plan writes; part 2 adds documents/deliverables/tasks without a
-- migration. The UNIQUE key is §7.1's (entity_kind, entity_id,
-- subject_revision); part 2 widens it when one revision needs several lanes.
CREATE TABLE approvals (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind      text NOT NULL,
    entity_id        text NOT NULL,
    subject_revision text NOT NULL,
    required_role    text,
    required_actor   text REFERENCES actors (id) ON DELETE RESTRICT,
    resolving_actor  text REFERENCES actors (id) ON DELETE RESTRICT,
    state            text NOT NULL CHECK
        (state IN ('awaiting', 'approved', 'rejected', 'changes_requested')),
    created_at       timestamptz NOT NULL,
    resolved_at      timestamptz,
    UNIQUE (entity_kind, entity_id, subject_revision)
);

CREATE INDEX approvals_awaiting_idx
    ON approvals (entity_kind, entity_id) WHERE state = 'awaiting';
