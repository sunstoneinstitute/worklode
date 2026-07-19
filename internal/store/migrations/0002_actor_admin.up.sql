-- Admin flag on actors: only admin actors may manage projects, actors, and
-- tokens. The bootstrap actor is created with admin = 1.
ALTER TABLE actors ADD COLUMN admin INTEGER NOT NULL DEFAULT 0;
