-- Each classification of an inline reference is a row, so a person's later
-- verdict is data (WL-REQ-1853, WL-REQ-1856).
BEGIN;

CREATE TABLE ref_classifications (
    id                 bigserial PRIMARY KEY,
    from_kind          text NOT NULL,
    from_id            text NOT NULL,
    field              text NOT NULL,
    from_version       integer,
    line               integer NOT NULL,
    line_text          text NOT NULL,
    to_kind            text NOT NULL,
    to_id              text NOT NULL,
    classifier         text NOT NULL CHECK (classifier IN ('cue','jev')),
    classifier_version integer NOT NULL,
    model              text,
    predicate          text NOT NULL,
    confidence         double precision NOT NULL,
    probabilities      jsonb,
    outcome            text NOT NULL DEFAULT 'kept' CHECK (outcome IN ('kept','adjusted','rejected','missed')),
    adjusted_to        text,
    outcome_by         text REFERENCES actors (id),
    outcome_at         timestamptz,
    created_at         timestamptz NOT NULL
);
CREATE INDEX ref_classifications_pair ON ref_classifications (from_kind, from_id, to_kind, to_id, created_at DESC);

ALTER TABLE task_edges       ADD COLUMN classification_id bigint REFERENCES ref_classifications (id);
ALTER TABLE rule_edges       ADD COLUMN classification_id bigint REFERENCES ref_classifications (id);
ALTER TABLE doc_edges        ADD COLUMN classification_id bigint REFERENCES ref_classifications (id);
ALTER TABLE task_governed_by ADD COLUMN classification_id bigint REFERENCES ref_classifications (id);

COMMIT;
