
| Edge | From | To | Meaning |
|---|---|---|---|
| `covers` | plan | rule | the plan undertakes to realise all of it. Resolved from the plan's `covers` entries when the plan is written |
| `defers` | plan | section, with owner | named planning gap until some plan covers it |
| `implements` | task (from a plan declaration) or component (from a repo manifest) | section | the work or code discharges the section |
| `amends` | rule | rule | the subject changes how the object is read without replacing it. Folded in by `lode show --inline`. Inverse `amendedBy`, never stored |
| `supersedes` | rule | rule | the subject replaces the object, which is withdrawn (`dct:replaces`). Inverse `supersededBy` (`dct:isReplacedBy`), never stored |
| `requires` | document | document | dependency. Inverse `isRequiredBy`, never stored |
| `blockedBy` | plan | plan | the subject runs after the object. A task is blocked while any task of the object plan is open. Inverse `blocksPlan`, never stored |

`amends` and `supersedes` are `rule_edges` rows. The other edges are `doc_edges` rows. Documents do not amend or supersede each other: a document arranges rules, and more than one document may arrange the same rule.

