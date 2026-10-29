
| Stored type | Table | Subject → object | Declared term | Inferred inverse |
|---|---|---|---|---|
| `covers` | `doc_edges` | plan → rule | `wl:covers` | none |
| `defers` | `doc_edges` | plan → section | `wl:defers` | none |
| `implements` | `doc_edges` | document → section | `wl:implements` | none |
| `requires` | `doc_edges` | dependent → required document | `dct:requires` | `dct:isRequiredBy` |
| `wasDerivedFrom` | `doc_edges` | derived → source document | `prov:wasDerivedFrom` | none |
| `blockedBy` | `doc_edges` | later plan → earlier plan | `wl:blockedByPlan` | `wl:blocksPlan` |
| `amends` | `rule_edges` | amending → amended rule | `wl:amends` | none |
| `supersedes` | `rule_edges` | new → old rule | `dct:replaces` | `dct:isReplacedBy` |
| `wasDerivedFrom` | `rule_edges` | derived → source rule | `prov:wasDerivedFrom` | none |
| `refines` | `rule_edges` | narrowing → narrowed rule | `wl:refines` | `wl:refinedBy` |
| `needs` | `rule_edges` | dependent → needed rule (WL-REQ-1367) | `wl:needs` | `wl:neededBy` |
| `references` | `inline_refs` | referring node → referenced node (doc, rule, task, milestone or deliverable) | `wl:references` | `wl:referencedBy` |
| `constrains` | `rule_edges` | the rule whose text states the relation → the other rule | `wl:constrains` | none |
| `conflictsWith` | `rule_edges` | the declaring rule → the other, once per pair | `wl:conflictsWith`, symmetric | itself |
| `dependsOn` | `task_edges` | dependent → prerequisite task | `wl:dependsOn` | `wl:blocks` |
| `childOf` | `task_edges` | child → parent task | `dct:isPartOf` | `dct:hasPart` |
| `followUpTo` | `task_edges` | follow-up → origin task | `wl:followUpTo` | none |
| `duplicateOf` | `task_edges` | duplicate → canonical task | `wl:duplicateOf` | none |

The stored task edge types use their ontology names, as `dependsOn` does, and the `wl:storedAs` annotations in `ns/ontology.ttl` match them. A typed row's `classification_id` is defined in WL-REQ-1853, and the `inline_refs` table in WL-REQ-1852.
