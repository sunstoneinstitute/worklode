
Enforced by the server:

| Rule | Statement | Enforcement |
|---|---|---|
| 1 | A rule's ref never changes, and a rule leaves a spec only through `lode rule unarrange` (WL-REQ-1297) or `lode rule supersede` (WL-REQ-1646). Section numbers and anchors are derived (WL-REQ-165), so a revision that renumbers sections or changes an anchor is accepted | no write path changes a ref or removes an arrangement entry any other way |
| 2 | A superseded section carries an explanation: a successor rule that `supersedes` its rule | detected, see below |
| 3 | `lastRevisedIn` is set on exactly the sections whose content changed. Touching it on unchanged sections mass-invalidates claims | refused |
| 4 | Anchor depth respects the configured limit | refused |

Rule 2's subject is a section already superseded, a fact about the corpus around the document. Refusing an unrelated accept would block the wrong edit. So it is derived: a section is superseded when its rule is withdrawn, and explained when another rule `supersedes` it. `lode doc list --bare-superseded` reports withdrawn rules with no successor.

Constraints bind from first acceptance. Never-accepted markdown is unconstrained.

