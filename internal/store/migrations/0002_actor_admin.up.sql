-- Admin flag on actors: only admin actors may manage projects, actors, and
-- tokens. The bootstrap actor is created with admin = 1.
ALTER TABLE actors ADD COLUMN admin INTEGER NOT NULL DEFAULT 0;

-- Backfill for databases bootstrapped before this migration existed: the
-- bootstrap actor is named 'admin' and must keep admin access.
UPDATE actors SET admin = 1 WHERE id = 'admin';
