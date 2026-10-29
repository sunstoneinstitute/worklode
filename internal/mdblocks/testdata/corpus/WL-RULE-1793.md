
A task minted from a plan should be `plannedIn` that plan (`tasks.plan_doc`); `plans` is the inverse, the plan's task set (WL-REQ-1669). A minted task loses the link only when someone edits it by hand.

A task minted from a plan must be governed by at least one rule. Two kinds of link count: a `task_governed_by` row (WL-REQ-1356) and an accepted invariant of the task's project, which governs every task in the project and is derived when the task is read. Tasks from a `covers: NO-SPEC` plan (WL-REQ-200) meet this through the project's invariants alone.

`lode doc accept` (WL-REQ-174) writes both links when it mints. A task from a covering plan gets `plan`-sourced links to every rule the plan covers.
