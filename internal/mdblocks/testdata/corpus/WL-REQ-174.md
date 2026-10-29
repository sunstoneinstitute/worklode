
`lode doc accept` on a plan runs one transaction: mint a `ready` task with `plan_doc` set for every declaration whose title names no task of the plan yet (soft-deleted tasks included, so a withdrawn task is not minted again), link each new task to the rules the plan covers (WL-REQ-1356), wire `blockedBy` between the new tasks as `blocks` edges, and set the plan `accepted`. Nothing is created above them. Three cases sit outside the plain path:

- **Historical import** records the status the plan reached and writes no task rows.
- **Re-acceptance after an edit** mints the declarations added since. A minted task is never changed or deleted by it, and no edge is wired into a task that already existed. Withdrawing published work is a task transition.
- **The coverage-only plan** has at least one `covers` or `defers` edge and no `## Tasks` section. It accepts and mints nothing, which puts its coverage in force.

A plan's body edit and its edge writes are its next version. Acceptance is keyed on document and version, so re-accepting an already accepted version mints nothing and answers with the document unchanged.

