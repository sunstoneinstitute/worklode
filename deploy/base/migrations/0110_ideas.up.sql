-- Ideas (WL-REQ-1799): a person's private idea, shared into a task when it is
-- sent for review. shared_at and task are set together.
CREATE TABLE ideas (
    id                      text PRIMARY KEY,
    author                  text NOT NULL REFERENCES actors (id),
    title                   text NOT NULL,
    body                    text NOT NULL DEFAULT '',
    type                    text NOT NULL CHECK (type IN ('general', 'story-pitch')),
    story_type              text NOT NULL DEFAULT '' CHECK (story_type IN ('data-story', 'short-story', '')),
    litmus_test_id          text NOT NULL DEFAULT '',
    shared_at               timestamptz,
    task                    text UNIQUE REFERENCES tasks (id),
    pre_research_authorized boolean NOT NULL DEFAULT false,
    decision                text NOT NULL DEFAULT '' CHECK (decision IN ('accept', 'narrow', 'park', 'stop', '')),
    deleted_at              timestamptz,
    created_at              timestamptz NOT NULL,
    updated_at              timestamptz NOT NULL,
    CHECK ((shared_at IS NULL) = (task IS NULL))
);

CREATE INDEX ideas_author_idx ON ideas (author) WHERE deleted_at IS NULL;
CREATE INDEX ideas_shared_idx ON ideas (shared_at) WHERE shared_at IS NOT NULL AND deleted_at IS NULL;

ALTER TABLE project_documents
    ADD CONSTRAINT project_documents_idea_fkey FOREIGN KEY (idea) REFERENCES ideas (id) ON DELETE CASCADE;
