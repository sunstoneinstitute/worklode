-- Reverse 0027_docs: drop tasks.plan_doc and this part's four tables, then
-- re-create the git→backbone sync on-ramp's three tables exactly as
-- 0011_docs.up.sql and 0012_doc_edges_covers.up.sql left them.

ALTER TABLE tasks DROP COLUMN plan_doc;

DROP TABLE doc_revisions;
DROP TABLE doc_edges;
DROP TABLE doc_sections;
DROP TABLE docs;

-- Spec 025 §5.1: the minimal document store the git→backbone sync populates.
-- Identity is (project, kind, ordinal), file-derived per 025 §16.3; doc_id is the
-- rendered <KEY>-SPEC-<n> / <KEY>-ADR-<n> / <KEY>-PLAN-<s>-<p> form, composed
-- server-side from the project's key. status is carried as data — the store
-- runs no editorial transitions (025 §5.1).

CREATE TABLE docs (
    project       text NOT NULL REFERENCES projects (id),
    kind          text NOT NULL CHECK (kind IN ('spec', 'adr', 'plan')),
    ordinal       text NOT NULL,
    doc_id        text NOT NULL,
    status        text NOT NULL,
    title         text NOT NULL,
    body          text NOT NULL,
    frontmatter   jsonb NOT NULL,
    version       integer NOT NULL DEFAULT 1,
    -- Sync provenance (025 §16.2): which branch the projection came from, and
    -- whether the tree was dirty — how a consumer tells a forced projection
    -- from a reviewed one.
    source_branch text NOT NULL,
    source_dirty  boolean NOT NULL,
    synced_at     timestamptz NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (project, kind, ordinal)
);

CREATE UNIQUE INDEX docs_doc_id ON docs (doc_id);

-- Anchored sections; specs and ADRs only - plans take none (025 §9).
CREATE TABLE doc_sections (
    project  text NOT NULL,
    kind     text NOT NULL,
    ordinal  text NOT NULL,
    anchor   text NOT NULL,
    heading  text NOT NULL,
    depth    integer NOT NULL,
    position integer NOT NULL,
    PRIMARY KEY (project, kind, ordinal, anchor),
    FOREIGN KEY (project, kind, ordinal)
        REFERENCES docs (project, kind, ordinal) ON DELETE CASCADE
);

-- Frontmatter relations (025 §5.1), section-scoped where an end is a section.
-- target is the raw corpus reference (a filename, repo-relative path, or the
-- NO-SPEC sentinel) - resolution stays a read-time concern. rel 'blocks' is
-- admitted for plans' document-level ordering edges even though no
-- frontmatter key emits it yet.
CREATE TABLE doc_edges (
    project       text NOT NULL,
    kind          text NOT NULL,
    ordinal       text NOT NULL,
    src_anchor    text NOT NULL DEFAULT '',
    rel           text NOT NULL CHECK (rel IN
        ('implements', 'covers', 'amends', 'amendedBy', 'replaces', 'isReplacedBy', 'blocks')),
    target        text NOT NULL,
    target_anchor text NOT NULL DEFAULT '',
    PRIMARY KEY (project, kind, ordinal, src_anchor, rel, target, target_anchor),
    FOREIGN KEY (project, kind, ordinal)
        REFERENCES docs (project, kind, ordinal) ON DELETE CASCADE
);
