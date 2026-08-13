-- Narrowing back to UNIQUE (worktree) validates existing rows, so the revert
-- fails loudly if two actors currently hold active leases on the same worktree
-- identity — exactly the state the up migration made legal. An operator must
-- release one of those leases, then re-run.
DROP INDEX leases_active_worktree;
CREATE UNIQUE INDEX leases_active_worktree
    ON leases (worktree) WHERE released_at IS NULL;
