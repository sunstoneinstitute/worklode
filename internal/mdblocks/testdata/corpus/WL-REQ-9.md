
A plan is the reconciliation between a spec and the code at the moment it was
written. Once its tasks are published, reality diverges from it in two ways:

- The "how" turned out different. The task records that, in its notes, its
  block reason, or the pull request itself. The task is the durable record of
  what was actually done, and the plan is not edited to match it.
- The "what" turned out different. That is a spec change, made through
  `lode doc edit` on the owning section. The edit marks every unexecuted plan
  covering that section `stale`, and re-planning edits the stale plan against
  the amended spec and the current code and re-accepts it (WL-SPEC-77 §10,
  §11).

A plan stays editable at any status. An edit to a plan with published tasks
must keep its task list parseable, and re-acceptance never changes or deletes a
published task (WL-SPEC-77 §11.2).

