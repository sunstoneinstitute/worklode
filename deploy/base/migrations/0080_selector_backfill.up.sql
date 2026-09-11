-- Repairs databases that recorded schema version 71 without applying
-- 0070_label_declarations: that migration landed on main after
-- 0071_entity_edges, so golang-migrate's Up() skips it forever on any
-- database that migrated in the window between the two (WL-847). No-op
-- where 0070 already ran.
ALTER TABLE artifact_declarations ADD COLUMN IF NOT EXISTS selector text NOT NULL DEFAULT 'address';

-- 0070 declared the CHECK inline, so Postgres generated its name; that name
-- is not guaranteed (it gains a numeric suffix if taken, and a hand-repaired
-- database may have chosen its own). Match on what the constraint constrains
-- rather than on its name, so an equivalent constraint under any name counts
-- and this never adds a second one.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint c
        JOIN pg_attribute a
          ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.conrelid = 'artifact_declarations'::regclass
          AND c.contype = 'c'
          AND a.attname = 'selector'
    ) THEN
        ALTER TABLE artifact_declarations
            ADD CONSTRAINT artifact_declarations_selector_check
            CHECK (selector IN ('address', 'label'));
    END IF;
END $$;
