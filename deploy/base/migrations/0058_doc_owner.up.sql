-- WL-382: a document's assignee is really an owner (spec 025, amended) — the
-- accept/land/withdraw authority gate, never a work queue like a task's
-- assignee. Rename the column to match.
ALTER TABLE docs RENAME COLUMN assignee TO owner;
CREATE INDEX docs_owner ON docs (owner) WHERE owner IS NOT NULL;
