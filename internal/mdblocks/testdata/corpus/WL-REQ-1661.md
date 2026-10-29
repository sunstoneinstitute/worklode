
Nothing links to a new IRI and the status never flips back to draft. A revision is a candidate version `v(n+1)` in status `draft` against the stable document identity, one candidate slot per document:

1. `lode doc revise <ref>` opens the candidate. Needs `doc.write`.
2. The accepted version stays current and authoritative. Readers and drift queries are unaffected.
3. Review with crit, comments anchored to sections.
4. `lode doc revise <ref> --accept` lands it: the constraint check of WL-REQ-167 and the publication transaction succeed or fail together. Owner only.
5. `lode doc revise <ref> --discard` (`DELETE /api/v1/docs/{id}/revision`) withdraws the candidate and frees the slot. Owner or the revision's author. The `doc.revision_discarded` event carries the withdrawn body.

`lode doc revise <ref> --file <path>` replaces the open candidate's body. Within a revision rules may be added, reworded, moved, unarranged (WL-REQ-1297) or marked superseded, and section numbers follow the new arrangement (WL-REQ-165).

