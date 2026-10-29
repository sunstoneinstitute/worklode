
The gates stay where they are; the review supplies evidence.

- Documents: not accepted until every `doc_reviewers` reviewer approves;
  `lode doc accept` reads `approvals` (WL-SPEC-77). A reviewer round on a
  document resolves that reviewer's `approvals` lane at the reviewed version.
- Changes: opening a review over a task's change moves it to `in_review`; a
  round carrying any open `change_request` thread, or a `changes_requested`
  verdict, returns it to `in_progress` and releases nothing, since the author
  holds the lease. The merge transition stays the task state machine's.

