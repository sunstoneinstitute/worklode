
An event from an outside service (data catalog, CMS, publisher) names an address or label. The first hop routes it to every open entity that declared that address or label: a deliverable, a task (`lode task edit <id> --artifact <address>`) or a document. An open task is live and not closed by `taskClosed`, which reads the repo mapping's `done_state`. The evidence lands as an `artifact_evidence` row with its own provenance (WL-SPEC-75 §13.3).

The second hop routes from a deliverable to the open tasks whose effective done declaration names it. This is why the effective declaration must be computable for any task, including one that is not being briefed.

