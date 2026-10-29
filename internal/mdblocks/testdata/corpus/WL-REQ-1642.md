
Every rule has exactly one kind, and each kind states one kind of obligation:

| Kind | Ref | Obligation | A reviewer checks it | Planning |
|---|---|---|---|---|
| `requirement` | `WL-REQ-<n>` | a plan builds it once. After that its tests hold the behavior and the rule still states what the behavior must be | directly against the change | a gap until an accepted plan covers it |
| `catalogue` | `WL-REQ-<n>` | a set of entries with one shape, where every entry answers the same question: a command tree, a metric table, an error table, a state list. A plan builds its entries | by membership: the change adds, removes or alters an entry | a gap until an accepted plan covers it |
| `invariant` | `WL-RULE-<n>` | a property that holds in every state, with the checks that enforce it. Binds every task in its project and is never finished | against the state, not the change | never covered, never a gap |
| `definition` | `WL-RULE-<n>` | one term, one meaning, in one scope (WL-REQ-1368) | whether the term is used as defined | never covered, never a gap |
| `principle` | `WL-RULE-<n>` | a design stance that other rules `refine`. Not checkable on its own | through its refiners only | never covered, never a gap |
| `capability` | `WL-RULE-<n>` | something people can do with the system: the subject that the rules refining it build. Not checkable on its own | through its refiners only | never covered directly. Covered when at least one requirement or catalogue refines it, directly or through other refiners, and every one of them is covered. A capability nothing builds is not covered |

An invariant controls state: it is violated by a state the system reaches, whatever change got it there, so it is enforced by checks at the writes that could break it (a 409 on an actor collision, a refused unarrange of an entry with deeper entries, a refused self-approval). Those checks are part of the invariant, not separate rules. A catalogue is distinguished from a list of unrelated decisions by its shape: a table whose entries have different shapes or answer different questions is several requirements, or one requirement with an illustrative table. Two catalogues in one body are two rules. A capability and a principle are both roots that other rules `refine`. A capability names what people can do ("manage Worklode from an MCP client"); a principle names a stance on how the system does things ("never pick a project silently"). Coverage, progress and search report by capability, so a feature with no capability rule is visible only through the spec that arranges it. Rationale is not a kind: a rule's body may end with a short rationale paragraph, and it never stands alone.

`informative` is retired. Background that other rules refine is a `principle`; background that nothing refines is template text of the spec (WL-REQ-1295). Existing informative rules are reclassified once (WL-REQ-1301); the kind still resolves on read until then.

A rule that an accepted plan covers (a covering plan counts through supersession) stays a requirement or catalogue: `lode rule set <ref> --kind invariant|definition|principle|capability` is refused with 409 until the plan is withdrawn or superseded.

The kind sets the ref's infix, and all kinds share the `RULE` counter, so the number alone identifies the rule. Any infix resolves any rule by its number: `WL-REQ-<n>` and `WL-RULE-<n>` with the same number are the same rule wherever a rule ref is read, including refs in document text. Output always prints the infix of the rule's current kind, so reclassifying a rule changes how its ref is printed and breaks no reference. A rule that both builds something and binds later work holds two obligations: split it into a requirement and an invariant.
