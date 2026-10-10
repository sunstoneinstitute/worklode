-- Every rule has one kind (WL-REQ-165). The column only: WL-936 sets the
-- real values, so every existing rule starts as a requirement.
ALTER TABLE rules ADD COLUMN kind text NOT NULL DEFAULT 'requirement'
    CONSTRAINT rules_kind_check CHECK (kind IN ('requirement', 'invariant', 'informative'));
