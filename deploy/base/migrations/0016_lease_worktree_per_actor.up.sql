-- Scope the active-worktree uniqueness to the actor holding the lease.
--
-- The index exists to stop one agent checking out two tasks in one directory.
-- Keyed on the bare worktree string it enforced something wider: that no two
-- actors anywhere share a worktree identity. That identity is
-- "<hostname>:<path>" (internal/worktree.Identity), so any two operators on
-- one hostname — devcontainers, a shared dev box, identically-named pods —
-- collide on their conventional layout, and the loser is told "worktree
-- already holds an active lease" about a task nobody on their machine
-- claimed. Keying on (actor_id, worktree) keeps the rule that matters and
-- drops the one that was never intended.
--
-- The index keeps its name: internal/store/leases.go maps a violation of
-- leases_active_worktree to ErrLeased with a worktree-specific message, and
-- that mapping is unchanged.
DROP INDEX leases_active_worktree;
CREATE UNIQUE INDEX leases_active_worktree
    ON leases (actor_id, worktree) WHERE released_at IS NULL;
