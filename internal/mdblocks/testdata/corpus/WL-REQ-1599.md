
Three things move with the tombstone of WL-REQ-117, in the deleting transaction: the lease is released and an `in_progress` task goes back to `ready`; the parent's roll-up is recomputed, on delete and on undelete; a document's slug and corpus number stop being reserved (uniqueness is over live rows, and a live row always wins over a tombstone that shares its slug). Delete does not cascade to children or covering plans.

