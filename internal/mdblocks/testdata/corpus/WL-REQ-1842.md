
`approval_decide(id, verdict?, note?, confirmed?)` calls `POST /api/v1/approvals/{id}/decide` and records a verdict only when the approver gave it. When the client speaks MCP protocol `2026-07-28` and supports form elicitation, the tool returns an input-required result (SEP-2322) that asks the approver directly, naming the entity, the revision being decided on and the approver, and offering approve, request changes and reject with an optional note. The client's retry of the call carries the answer, and an accept decides the approval. Otherwise the tool answers with the same facts and choices and the instruction to ask with the question tool, or read them aloud in voice, and call again with that verdict and `confirmed=true`. A call with `verdict` and without `confirmed=true` decides nothing.

A reviewer's verdict on a spec or plan is an approval decision: `doc_reviewers` names the reviewers, `approval_request` asks them, and `approval_decide` records each verdict.

A decide refused because the approver's group claims are stale answers with the instruction to reconnect the connector, which logs in again.

Why: an approval records that a person decided. Elicitation gets the verdict from the person without the model writing it, and the question tool is the fallback where the client lacks it. A stateless server cannot send a request to the client, so elicitation needs the input-required result of protocol `2026-07-28`.

