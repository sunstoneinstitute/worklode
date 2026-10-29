
Every inline reference edge, whatever its node kinds, is a row in one table,
`inline_refs`, owned by what holds the field (WL-REQ-1288):

| Column | Meaning |
|---|---|
| `from_kind`, `from_id` | what holds the field: a doc, rule, task, milestone, deliverable, idea, decision or approval |
| `field` | the field's name (WL-RULE-1850) |
| `from_version` | the node's version whose text the line numbers point into; null when the field is not versioned (task fields, notes, descriptions) |
| `to_kind`, `to_id` | the target node; a section ref is stored as the node it resolved to (WL-RULE-1849) |
| `via_doc` | for a rule reached through a doc fragment, the doc it was cited in, so the (spec, rule) context survives; otherwise null |
| `via_anchor` | for a `#sec-` fragment, the anchor as written, so a later write can keep the earlier resolution instead of resolving the anchor again; otherwise null |
| `lines int[]` | one entry per occurrence, in ascending order; a line that mentions the target twice appears twice, so an edit that removes one of two references on a line changes the row |
| `source` | `manual` (written by a person) or `derived` (written by extraction) |

The key is (`from_kind`, `from_id`, `field`, `from_version`, `to_kind`,
`to_id`, `via_doc`, `via_anchor`). The ends are (node kind, id) pairs with no foreign
keys, so deleting a node deletes the rows on either end of it in the same
transaction.

Rows for every version are kept in this one table, so there is no separate
snapshot table. A write that creates a new version adds that version's rows
and leaves the older versions' rows as they were. A write that rewrites a
version in place (a draft) replaces that version's `derived` rows. A field
with no versions has its `derived` rows replaced on every write. The current
edges of a node are its rows whose `from_version` is the node's current
version or null. The derived `references` rows in `rule_edges` move into this
table, and `rule_edges` stops storing `references`.

Every write of a `model.Markdown` field re-extracts it in the same
transaction. For a `#sec-` fragment, re-extraction looks for a row of the
field's previous text with the same `via_doc` and `via_anchor` and keeps its
target; only a fragment that is new in this write is resolved against the
current arrangement. A `manual` row is never touched. A derived row
duplicates a manual one when both have the same holder, field, version and
target, whatever their `via_doc` and `via_anchor`; it is not written, and its
lines are recorded on the manual row instead.
