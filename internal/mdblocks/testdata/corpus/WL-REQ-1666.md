
The server decides the mechanical part at `lode doc edit` time and refuses a silent patch that trips a rule, naming it. The fixer judges the rest, and uncertain counts as substantive.

| Class | Rules |
|---|---|
| Mechanically substantive (server) | section has a referrer; edit adds a dependency; edit touches anything mirrored in `ns/`; edit changes a schema, migration, API, CLI flag, event name, IRI or enum; edit changes acceptance criteria or a definition of done |
| Judged substantive (fixer) | removes, reverses or narrows a normative statement; changes a default or threshold; adds a requirement implying work the plan lacks; contradicts another section |
| Non-substantive (only these) | wording that changes no assertion; an added example, typo, repaired reference or anchor; filling a gap the document was silent on, following a principle stated elsewhere in it |

A `patched` section leaves the document `accepted`. `lode doc show` and `lode task brief` render it with its notes inline. "How much of this document is still what a human approved" is a query.

