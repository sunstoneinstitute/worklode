-- Inline references get their own table, and a version records whether it is
-- an edit or a normalization (WL-REQ-1852, WL-REQ-1855). Rule references stay
-- in rule_edges until the store writes them here; the move is a later
-- migration shipped with that writer.
-- from_id and to_id are text: tasks use WL-7-style ids, the rest bigint.
BEGIN;

CREATE TABLE inline_refs (
    from_kind    text NOT NULL CHECK (from_kind IN ('doc','rule','task','milestone','deliverable')),
    from_id      text NOT NULL,
    field        text NOT NULL,
    from_version integer,
    to_kind      text NOT NULL CHECK (to_kind IN ('doc','rule','task','milestone','deliverable')),
    to_id        text NOT NULL,
    lines        integer[] NOT NULL,
    source       text NOT NULL CHECK (source IN ('manual','derived')),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX inline_refs_key ON inline_refs
    (from_kind, from_id, field, COALESCE(from_version, -1), to_kind, to_id);
CREATE INDEX inline_refs_to ON inline_refs (to_kind, to_id);

ALTER TABLE doc_versions  ADD COLUMN change text NOT NULL DEFAULT 'edit' CHECK (change IN ('edit','normalize'));
ALTER TABLE rule_versions ADD COLUMN change text NOT NULL DEFAULT 'edit' CHECK (change IN ('edit','normalize'));

COMMIT;
