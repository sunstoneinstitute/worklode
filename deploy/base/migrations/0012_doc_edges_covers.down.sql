-- Re-adding the six-rel CHECK validates existing rows, so the revert fails
-- loudly if any 'covers' edge survives rather than leaving an unrepresentable
-- edge; an operator must remove or re-project those edges, then re-run.

ALTER TABLE doc_edges DROP CONSTRAINT doc_edges_rel_check;
ALTER TABLE doc_edges ADD CONSTRAINT doc_edges_rel_check
    CHECK (rel IN
        ('implements', 'amends', 'amendedBy', 'replaces', 'isReplacedBy', 'blocks'));
