
Every reference that a Markdown field of a Worklode doc, rule, task,
milestone, deliverable, idea, decision or approval makes to a doc, rule, task,
milestone or deliverable (a node) is stored as an edge, so a reader can find
everything that refers to a given node without searching text.
The edge records which field and which lines hold the reference. Which
relation the edge carries comes from the text: a plain reference is
`references`, and a stronger relation (`dependsOn`, `refines`, `governedBy`
and the rest) is written by a classifier. A person repairs a wrong one by
changing or deleting the edge, or by re-running a newer classifier, and each
repair is counted against the classifier that wrote it.
