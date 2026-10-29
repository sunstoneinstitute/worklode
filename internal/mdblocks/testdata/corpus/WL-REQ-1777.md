
- An unresolved `change_request` thread on the current round refuses an
  `approved` verdict; an open `note` or `question` does not. Thread resolution
  and verdict are orthogonal signals sharing a UI.
- A new round from the author clears every approval cast against an earlier
  round: a reviewer round counts only while its `base_ref` is the review's
  current one. Nothing on the old rounds is updated or deleted.

