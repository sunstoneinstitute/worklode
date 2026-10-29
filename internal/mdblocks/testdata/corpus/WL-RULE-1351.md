
Nothing in this spec is built: there are no `reviews` or `review_*` tables and
no `internal/review`, and `lode doc note` still writes `doc_notes`. These plans
build it:

- WL-PLAN-132: the review record and document review over the API (§1, §3, §5–§7, §10).
- WL-PLAN-133: change review, `task_reviewers`, surviving anchors, the `in_review` loop (§1, §2, §4.1, §7).
- WL-PLAN-134: guides, `PUT .../guide`, the honesty rule (§4, §4.1, §9).
- WL-PLAN-135: the crit bridge, the anchor sidecar, the read-only web view (§1.1, §6).
- The skill that carries §8's floor list to the worker: WL-969, after WL-PLAN-133.
