
`lode show WL-RULE-<n>` (`lode rule show`) and `GET /api/v1/rules/WL-RULE-<n>` read a rule with its current text, the documents arranging it and the plans covering it. `GET /api/v1/rules/WL-RULE-<n>/versions`, `/versions/<v>`, `lode rule versions` and `lode show WL-RULE-<n> --version <v>` read the history. `lode rule list [--doc <ref>] [--status <s>]` lists rules. Tasks link to rules through `governedBy` (WL-REQ-1356). More than one document may arrange the same rule.

