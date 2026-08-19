-- Documents in the backbone (docs/specs/025-documents-in-the-backbone.md §5).
--
-- Replaces the git→backbone sync on-ramp's tables from 0011_docs and
-- 0012_doc_edges_covers. That subsystem (025 §16, superseded) projected files
-- into rows keyed (project, kind, ordinal) and carried sync provenance; this
-- one authors documents in the backbone and needs a single-column identity
-- that tasks.plan_doc and doc_edges.to_doc can reference. The tables are
-- empty, so this is a replacement and not a data migration.
--
-- The status CHECK mirrors ns.DesignDocStatuses (generated from ns/concept.ttl
-- once WL-70 lands); the kind CHECK mirrors the wl:Spec/wl:ADR/wl:Plan classes.

DROP TABLE doc_edges;
DROP TABLE doc_sections;
DROP TABLE docs;

CREATE TABLE docs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id  text NOT NULL REFERENCES projects(id),
    kind        text NOT NULL CHECK (kind IN ('spec','adr','plan')),
    number      integer,          -- corpus number; NULL for plans (025 §14.3)
    slug        text NOT NULL,
    title       text NOT NULL,
    body        text NOT NULL,    -- the full markdown, frontmatter included
    status      text NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft','accepted','superseded')),
    version     integer NOT NULL DEFAULT 1,
    issued      date,
    assignee    text REFERENCES actors(id),
    created_by  text REFERENCES actors(id),
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    CHECK (kind = 'plan' OR number IS NOT NULL)
);
CREATE UNIQUE INDEX docs_project_kind_number
    ON docs (project_id, kind, number) WHERE number IS NOT NULL;
CREATE UNIQUE INDEX docs_project_slug ON docs (project_id, slug);

-- Specs and ADRs only (025 §9: plans carry no sections and no anchors).
CREATE TABLE doc_sections (
    doc_id          bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    anchor          text NOT NULL,
    number          text,
    heading         text NOT NULL,
    depth           integer NOT NULL,
    position        integer NOT NULL,
    last_revised_in integer NOT NULL DEFAULT 1,   -- 025 §4.4
    published       boolean NOT NULL DEFAULT false, -- frozen from first accept (025 §6)
    PRIMARY KEY (doc_id, anchor)
);

-- One row carries both directions: amends read backward is amendedBy, so the
-- 025 §14 mirror cannot disagree by construction. to_external holds a
-- cross-corpus shorthand this backbone cannot resolve (025 §14.3).
-- covers is a plan's promise about spec sections; implements is a component's
-- evidence about its code and covers's retired spelling (026 §5.1, §6.2).
CREATE TABLE doc_edges (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_doc    bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    from_anchor text,
    type        text NOT NULL CHECK (type IN
                ('covers','implements','amends','replaces','requires','wasDerivedFrom','blocks')),
    to_doc      bigint REFERENCES docs(id),
    to_anchor   text,
    to_external text,
    CHECK ((to_doc IS NULL) <> (to_external IS NULL)),
    -- blocks orders whole plan documents (025 §5): never section-scoped.
    CHECK (type <> 'blocks' OR (from_anchor IS NULL AND to_anchor IS NULL))
);
CREATE UNIQUE INDEX doc_edges_unique ON doc_edges
    (from_doc, coalesce(from_anchor,''), type,
     coalesce(to_doc, 0), coalesce(to_anchor,''), coalesce(to_external,''));
CREATE INDEX doc_edges_to ON doc_edges (to_doc) WHERE to_doc IS NOT NULL;

-- One open candidate revision per doc (025 §7: the candidate carries draft
-- implicitly by being here).
CREATE TABLE doc_revisions (
    doc_id     bigint PRIMARY KEY REFERENCES docs(id) ON DELETE CASCADE,
    body       text NOT NULL,
    created_by text REFERENCES actors(id),
    created_at timestamptz NOT NULL
);

-- 025 §9.2: nullable by design — a task no plan authored carries none. The
-- plan task format's `skills` land in the existing tasks.skills jsonb
-- column (migration 0007), so no new column is needed for them.
ALTER TABLE tasks ADD COLUMN plan_doc bigint REFERENCES docs(id);
CREATE INDEX tasks_plan_doc ON tasks (plan_doc) WHERE plan_doc IS NOT NULL;
