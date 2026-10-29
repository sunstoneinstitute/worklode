
| Action | Where | Does |
|---|---|---|
| Accept | each `draft` plan line and a `draft` spec row | exactly what `lode doc accept` does: owner gate, reviewer gate, task minting in one transaction. Enabled only for the document's owner; a refusal is a 409 with the store's reason. |
| Review | every spec row and plan line | opens a WL-SPEC-84 document review through WL-SPEC-84's own review-create route (`POST /api/v1/reviews`). The server decides per request whether that route is registered; while it is not, the button is disabled with hover text saying the review surface is not built. |
| Plan | a spec row with an `unplanned` section | if an open `kind = 'design'` task with `about_doc` the spec exists, the row shows "Planning: WL-nnn" as a link and no button; otherwise mints one with the doc-lifecycle subscriber's title and guard |
| Rally | every spec row | adds the spec's remaining acts to the project's draft rally (WL-SPEC-85#sec-4b) |
| Confirm Rally, Discard | the rally footer | publishes the draft (`draft` to `ready`, the activation act) or abandons it, dropping its edges |
| Queue for merge, Merge | a pinned task tooltip with an open PR | enqueues or merges the PR (WL-SPEC-85#sec-4c) |

Rally, Confirm Rally and Discard are disabled without a signed-in session.

