
A decide may carry a note. A decide of `changes_requested` or `rejected` must carry one, and a decide without it is refused with 422 on every surface: the cockpit, `POST /api/v1/approvals/{id}/decide`, `lode approval decide` and the MCP server's `approval_decide`.

The note is stored on the approvals row as `decision_note`, beside `resolving_actor` and `resolved_at`. It is separate from the impact note of WL-REQ-118. Every reader of the row returns it: `GET /api/v1/approvals`, `lode approval list`, the cockpit queue and `approval_list`. A row decided again after `changes_requested` replaces the note. The `approval.decided` event keeps each note, so earlier ones stay in the history.

Rationale: the author of the work has to know why it was sent back or refused, and the row is where every surface already reads the decision.
