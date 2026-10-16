-- Documents register (WL-REQ-1798): pointers to files, datasets and links a
-- project or an idea works from. idea has no fk yet: the migration that
-- creates ideas (WL-REQ-1799) adds it.
CREATE TABLE project_documents (
    id           text PRIMARY KEY,
    project      text REFERENCES projects (id) ON DELETE CASCADE,
    idea         text,
    name         text NOT NULL,
    category     text NOT NULL CHECK (category IN ('spec', 'plan', 'meeting-notes', 'deliverables', 'sources')),
    type         text NOT NULL CHECK (type IN ('document', 'spreadsheet', 'pdf', 'slides', 'dataset', 'link')),
    status       text NOT NULL CHECK (status IN ('draft', 'in-review', 'final')),
    url          text NOT NULL DEFAULT '',
    source       text NOT NULL DEFAULT '',
    milestone    text NOT NULL DEFAULT '',
    due          date,
    date         date,
    participants text[] NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    created_by   text REFERENCES actors (id) ON DELETE RESTRICT,
    CHECK ((project IS NULL) <> (idea IS NULL))
);

CREATE INDEX project_documents_project_idx ON project_documents (project);
CREATE INDEX project_documents_idea_idx ON project_documents (idea);
