
Submitting is the owner's deliberate act of acceptance. It states that the document is to be accepted once review passes, so no second act is needed after the last approval.

- The decide transaction that settles the last open approval on a submitted version accepts the document in the same transaction, when the reviewer gate of WL-REQ-1658 holds. It runs the same acceptance `lode doc accept` runs (a plan's acceptance mints its tasks as drafts), records the owner as the accepting actor, and emits `wl:DocumentAccepted` informed by the deciding approval.
- A submission with no assigned reviewers and no open approval is accepted at submit, in the submit transaction.
- `lode doc accept` stays: the owner may accept a draft directly when the gate holds, which covers a document that was never submitted.
- A requested change or a rejection ends the submission. The owner revises and submits again.
- If acceptance fails (a depth violation, a malformed plan), the approval still lands and the document stays draft. The failure is recorded on the open review task, and the owner fixes and resubmits.

No subscriber performs acceptance (WL-REQ-109). Decomposing an accepted spec into plans is chosen explicitly. Skills offer that step, never take it.

Rationale: a reviewer's approval and the owner's accept were two acts for one decision. The owner's intent is already expressed by submitting, and a second click after review only adds a step that is forgotten or done without looking.
