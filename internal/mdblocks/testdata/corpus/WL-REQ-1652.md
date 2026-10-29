
Header keys are ontology property local names. A key with no term signals that the ontology is missing one. An unknown key is an error.

| Key | Term | Value |
|---|---|---|
| `status` | `wl:status` | one `wlc:DesignDocStatus` concept |
| `issued` | `dct:issued` | ISO date of first publication |
| `covers` | `wl:covers` | requirements a plan undertakes to build: a rule ref, `<doc>#sec-N` or a whole document, resolved to requirements when the plan is written (WL-SPEC-77#sec-4p). Plans only |
| `defers` | `wl:defers` | spec sections a plan hands to a named owner (`spec:`/`to:`). Plans only, after `covers` in key order |
| `requires` | `dct:requires` | dependency |
| `blockedBy` | `wl:blockedByPlan` | document-level plan ordering, declared on the later plan. Plans only |
| `wasDerivedFrom` | `prov:wasDerivedFrom` | the design record this graduated from |
| `skills` | `wl:recommendsSkill` | skills the design recommends (WL-SPEC-81#sec-7) |

`amends`, `amendedBy`, `replaces` and `isReplacedBy` are not keys: amendment and supersession are rule edges (WL-SPEC-77#sec-4m). A header carrying one is refused, naming the rule edge commands. `blocks` and `isRequiredBy` are inverse spellings and are refused too (WL-REQ-1288): declare `blockedBy` on the later plan and `requires` on the dependent document.

`blockedBy` is the one key whose spelling differs from its term. `wl:blockedByPlan` is a distinct Plan-to-Plan property so the Task-to-Task `wl:dependsOn` closure stays type-homogeneous and `?t wl:dependsOn+ ?x` never walks document ordering.

Each `covers` entry is a plain reference, and covering means building the whole rule (WL-SPEC-77#sec-4p). `implements` in a plan header parses as `covers`. A plan with no governing spec writes `covers: NO-SPEC`. Coverage queries are in WL-SPEC-78#sec-4.

