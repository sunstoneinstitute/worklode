
- A `secrets` name list on the task (`lode task add --secrets A,B`, settable at create and update). The task model is in WL-SPEC-75.
- `secrets: [NAME, …]` in design-doc frontmatter (WL-SPEC-77).
- Resolution at claim: task pins, union governing-doc pins, union the baseline set.
- A declared name missing from the catalog is a brief warning, never a failure.

Declaring is the planner's job. The `lode-secrets` skill makes that a standing instruction.

