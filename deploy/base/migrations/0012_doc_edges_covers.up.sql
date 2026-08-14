-- Widen the doc_edges rel enum by 'covers' (spec 026). A plan's projected
-- edge to a spec section is wl:covers, not wl:implements — 026 §6.2 splits the
-- two, reserving implements for a component's evidence about its code. The
-- corpus loader (internal/designdoc) now emits 'covers' for plan edges; keep
-- 'implements' for components. No rows change. After this the CHECK and
-- store.validDocEdgeRels hold the same seven rels.

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_rel_check;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_rel_check
    CHECK (rel IN
        ('implements', 'covers', 'amends', 'amendedBy', 'replaces', 'isReplacedBy', 'blocks'));
