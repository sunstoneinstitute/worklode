-- Spec 029 §1: free-form labels stamped at promotion for classification
-- rules to act on (kind=sunstone-story is the first with meaning), and the
-- bounded/standing horizon attribute. No seeded_by column -- that reference
-- is an entity_edges row (029 §5, part 2's table).
ALTER TABLE projects ADD COLUMN labels  jsonb NOT NULL DEFAULT '{}';
ALTER TABLE projects ADD COLUMN horizon text NOT NULL DEFAULT 'standing'
    CHECK (horizon IN ('bounded','standing'));
