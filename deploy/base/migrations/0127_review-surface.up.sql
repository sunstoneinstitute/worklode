-- WL-REQ-1322: the review surface. GitHub's own reviews are github_reviews.
BEGIN;

CREATE TABLE reviews (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id   text REFERENCES tasks (id),
    doc_id    bigint REFERENCES docs (id),
    rule_id   bigint REFERENCES rules (id),
    subject   text NOT NULL CHECK (subject IN ('change', 'doc', 'rule')),
    state     text NOT NULL DEFAULT 'open'
              CHECK (state IN ('open', 'changes_requested', 'approved', 'closed')),
    base_ref  text NOT NULL,
    round     integer NOT NULL DEFAULT 1,
    next_seq  integer NOT NULL DEFAULT 1,
    opened_by text NOT NULL REFERENCES actors (id),
    opened_at timestamptz NOT NULL,
    closed_at timestamptz,
    CONSTRAINT reviews_one_subject CHECK (num_nonnulls(task_id, doc_id, rule_id) = 1),
    CONSTRAINT reviews_subject_matches CHECK (
        (subject = 'change' AND task_id IS NOT NULL)
        OR (subject = 'doc' AND doc_id IS NOT NULL)
        OR (subject = 'rule' AND rule_id IS NOT NULL))
);
CREATE UNIQUE INDEX reviews_open_task ON reviews (task_id)
    WHERE task_id IS NOT NULL AND state <> 'closed';
CREATE UNIQUE INDEX reviews_open_doc ON reviews (doc_id)
    WHERE doc_id IS NOT NULL AND state <> 'closed';

CREATE TABLE task_reviewers (
    task_id     text NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    actor_id    text NOT NULL REFERENCES actors (id) ON DELETE RESTRICT,
    assigned_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, actor_id)
);

CREATE TABLE review_rounds (
    review_id    bigint NOT NULL REFERENCES reviews (id),
    round        integer NOT NULL,
    actor_id     text NOT NULL REFERENCES actors (id),
    client       text NOT NULL,
    base_ref     text NOT NULL,
    focused      boolean NOT NULL DEFAULT false,
    overall_note text NOT NULL DEFAULT '',
    verdict      text NOT NULL,
    submitted_at timestamptz NOT NULL,
    PRIMARY KEY (review_id, round),
    CONSTRAINT review_rounds_client_known CHECK (client IN ('crit', 'web', 'cli', 'agent')),
    CONSTRAINT review_rounds_verdict_known CHECK (verdict IN ('approved', 'changes_requested'))
);

CREATE TABLE review_guides (
    review_id         bigint NOT NULL REFERENCES reviews (id),
    round             integer NOT NULL,
    guide             jsonb NOT NULL,
    proposed_by       text NOT NULL REFERENCES actors (id),
    skims_refused     integer NOT NULL DEFAULT 0,
    from_reading_diff boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL,
    PRIMARY KEY (review_id, round)
);

CREATE TABLE review_threads (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    review_id         bigint REFERENCES reviews (id),
    doc_id            bigint REFERENCES docs (id),
    rule_id           bigint REFERENCES rules (id),
    task_id           text REFERENCES tasks (id),
    version           integer,
    byte_start        integer,
    byte_end          integer,
    quote             text,
    doc_anchor        text,
    repo              text,
    path              text,
    side              text CHECK (side IN ('additions', 'deletions')),
    line_number       integer,
    anchor_text       text,
    blob_sha          text,
    raised_by_task    text REFERENCES tasks (id),
    raised_by_session text,
    intent            text NOT NULL,
    intent_classifier text,
    intent_confidence double precision,
    status            text NOT NULL DEFAULT 'open',
    author_id         text NOT NULL REFERENCES actors (id),
    created_at        timestamptz NOT NULL,
    resolved_by       text REFERENCES actors (id),
    resolved_at       timestamptz,
    CONSTRAINT review_threads_one_subject CHECK (num_nonnulls(doc_id, rule_id, task_id) = 1),
    CONSTRAINT review_threads_range_together CHECK (
        num_nonnulls(version, byte_start, byte_end, quote) IN (0, 4)),
    CONSTRAINT review_threads_intent_known CHECK (
        intent IN ('note', 'change_request', 'question')),
    CONSTRAINT review_threads_classifier_known CHECK (
        intent_classifier IS NULL OR intent_classifier IN ('jev', 'default')),
    CONSTRAINT review_threads_status_known CHECK (status IN ('open', 'resolved', 'obsolete'))
);
CREATE INDEX review_threads_open ON review_threads (review_id) WHERE status = 'open';
CREATE INDEX review_threads_doc_anchor ON review_threads (doc_id, doc_anchor);
CREATE INDEX review_threads_rule_version ON review_threads (rule_id, version);

CREATE TABLE review_messages (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    thread_id  bigint NOT NULL REFERENCES review_threads (id),
    review_id  bigint REFERENCES reviews (id),
    seq        integer NOT NULL,
    actor_id   text NOT NULL REFERENCES actors (id),
    role       text NOT NULL CHECK (role IN ('reviewer', 'author')),
    body       text NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX review_messages_seq_review ON review_messages (review_id, seq)
    WHERE review_id IS NOT NULL;
CREATE UNIQUE INDEX review_messages_seq_thread ON review_messages (thread_id, seq)
    WHERE review_id IS NULL;

COMMIT;
