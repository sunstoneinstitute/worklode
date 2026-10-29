
Informative.

- Condition 1: the offline check never reads the task or its plan (§5). WL-964.
- Condition 2 in CI: the check does not resolve the ref or check it is not withdrawn (§5). WL-964.
- Condition 3: the check accepts `amended` and compares no versions (§5). WL-964.
- Condition 4: the path restriction for `tests` and `build` (§5). No task yet.
- A CI comment quoting the cited section's first paragraph under the diff. WL-964.
- A `spec_gate` event on the task carrying the classification, the cited section and the pull request. It is the audit trail behind §2.3 and the input to a drift query over tasks that landed against a guarded path with `none` classifications, grouped by path. WL-964.


