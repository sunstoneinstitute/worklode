
A **done declaration** is an ordered list of **done references**. Both a project and a task carry one. A done reference is either a deliverable id (`<KEY>-DEL-<n>`, a row in `deliverables`) or one of exactly two **reserved references**, `merge` and `manual`. Reserved references are checks with no row behind them. They are lowercase and deliverable ids are uppercase with a `-DEL-` infix, so the two never collide. The reserved set is closed. A third reserved word is a change to this spec.

Merge is a reserved reference because a deliverable is a named output the project ships once, while a merge is a fact about one task's own branch.

