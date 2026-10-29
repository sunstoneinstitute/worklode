
A relation stronger than a reference (`dependsOn`, `refines`, `governedBy`
and the rest) stays in its typed table (`task_edges`, `rule_edges`,
`doc_edges`, `task_governed_by`). The readers that act on it (blockers, claim
order, `--closure`, governance, coverage) keep reading one table with its
foreign keys. Those tables gain one nullable column, `classification_id`, set
when a classification wrote the row. A typed row with `classification_id` set
whose target no longer appears in any field of its node is removed on the next
write of that node.
