
Delivery does not close the lease. The lease ends only on `release`, `abandon`, `reopen`, or the expiry sweep (WL-REQ-94). Mutual exclusion is already enforced by `Claim` requiring `ready`, and liveness by the TTL sweep. Work legitimately continues in a worktree after its branch is deployed to dev.

