
| Capability | Section | Built by |
|---|---|---|
| Task declarations become draft task rows written with `lode task add --plan`, published by accept; existing `## Tasks` sections converted, the format retired | 11.1, 11.2 | WL-PLAN-145: WL-916, WL-917, WL-918 |
| The client parses the header; `POST /api/v1/docs` takes structured fields and the server reads no YAML | 7 | WL-PLAN-145: WL-915, WL-919 |
| Task ordering stored as `dependsOn`; `task_edges` still stores `blocks` | 8.1 | WL-PLAN-145: WL-908 |
| Each edge table's `CHECK` and write path held to `ns/` by test; projection inverses read from `ns/` | 8.1, 14 | WL-PLAN-145: WL-910 |
| `lode doc impact <ref>[#sec-N] [--json]` (`GET /api/v1/docs/{id}/impact`): downstream documents one hop out, their open tasks, and the post-edit skill | 17 | WL-PLAN-141 |
| `lode doc impact --paths`: backtick-quoted repo paths missing from the checkout | 17 | WL-PLAN-142 |
| Rules are the record: spec headings without rules, `lode rule add`, `accept`, `arrange`, `unarrange`, rule edits on unarranged or shared rules, spec bodies rendered from rules, `lode doc show --editable`, rule-first guidance | 19.1–19.6 | WL-PLAN-146 (draft) |
| One migration converting stored spec bodies to rules and spec headings | 19.7 | WL-PLAN-146 (draft) |
| Rule kinds `catalogue`, `definition`, `principle`; `informative` retired | 4 | no plan yet |
| `needs` edge; inferred `refinedBy`, `neededBy`, `referencedBy`; `lode show --closure` | 4c, 8.1 | no plan yet |
| Template text in spec bodies, rendered around the arranged rules | 19.1, 19.5 | no plan yet |
| Glossary specs per project and per instance; `/project/<name>/term/<slug>` | 4d | no plan yet |
| Reclassifying informative rules, moving template text out of rule bodies, minting definitions | 19.7 | no plan yet |
| Rendered documents show each section's rule ref under its heading and outline a rule's sections on hover | 4 | WL-900 |
| `doc_notes` moves into `review_threads`; `lode doc note` writes a review thread | 10 | WL-PLAN-132 (draft) |
| Derived numbering: arrangement entries store position and depth only, numbers and `sec-N` anchors derived at render, every rule heading rendered with its ref, renumbering accepted at the accept gate, unarrange on an accepted spec substantive and refused over deeper entries | WL-REQ-165, WL-REQ-167, WL-REQ-1297, WL-REQ-1299 | WL-PLAN-150 |
| The `Spec:` trailer names a rule; a section value is refused naming the rule ref | WL-REQ-7 | WL-PLAN-150 |
| The graph projection emits rule IRIs and no section nodes; SHACL shapes read rules | WL-REQ-247, WL-REQ-1705 | WL-PLAN-150 |
| Hook enforcement of citing rules by ref: session-start and subagent-start context, the `commit-msg` check, the gate check on the pull request body and added lines | WL-REQ-1791 | WL-PLAN-150 |
