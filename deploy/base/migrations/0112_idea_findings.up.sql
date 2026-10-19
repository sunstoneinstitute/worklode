-- Idea findings (WL-REQ-1800): litmus-test results and pre-research runs
-- recorded on an idea. items holds a litmus test's recommendations or a
-- pre-research run's findings, as a JSON array of strings.
CREATE TABLE idea_findings (
    id         bigserial PRIMARY KEY,
    idea       text NOT NULL REFERENCES ideas (id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('litmus', 'pre_research')),
    date       date NOT NULL,
    summary    text NOT NULL DEFAULT '',
    items      jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by text REFERENCES actors (id) ON DELETE RESTRICT
);

CREATE INDEX idea_findings_idea_kind_date_idx ON idea_findings (idea, kind, date DESC);
