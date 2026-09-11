-- Repairs databases that recorded schema version 71 without applying
-- 0070_label_declarations: that migration landed on main after
-- 0071_entity_edges, so golang-migrate's Up() skips it forever on any
-- database that migrated in the window between the two (WL-847). No-op
-- where 0070 already ran.
ALTER TABLE artifact_declarations ADD COLUMN IF NOT EXISTS selector text NOT NULL DEFAULT 'address';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'artifact_declarations'::regclass
          AND contype = 'c'
          AND conname = 'artifact_declarations_selector_check'
    ) THEN
        ALTER TABLE artifact_declarations
            ADD CONSTRAINT artifact_declarations_selector_check
            CHECK (selector IN ('address', 'label'));
    END IF;
END $$;
