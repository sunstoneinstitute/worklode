
An inline reference is one occurrence of a node ref (doc ref, rule ref, task
id, milestone ref or deliverable ref) written in a Markdown field. It is
identified by the doc, rule, task, milestone, deliverable, idea, decision or
approval that owns the field, the field, the node's version
(none when the field is not versioned), the 1-based line number within the
field, and the resolved target node. A `#rule-<n>` fragment resolves to that
rule, with the doc as its context; when no rule `n` exists in the doc's
project, the reference is unresolved (WL-REQ-1851), not an edge to the doc. A `#sec-<anchor>` fragment is resolved once,
in the write that first adds it to the field: to the rule arranged at that
anchor, with the doc as its context, or to the doc when no rule is arranged
there. A later write keeps that resolution. A `#sec-` fragment that was in the
field before any write resolved it (text older than inline references)
resolves to the doc.
