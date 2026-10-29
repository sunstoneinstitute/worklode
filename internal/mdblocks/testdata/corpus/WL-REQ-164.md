
Documents are Postgres rows wrapped in the same event-logged transactions as tasks. The knowledge graph receives them by projection (WL-SPEC-79#sec-12). The graph's copy is never an authoring surface.

| Table | Holds |
|---|---|
| `docs` | identity (project, kind, number, slug), title (authoritative, WL-SPEC-77#sec-7b), body (plain markdown with no header), status, owner, `issued`, version counter. A spec's body and its rules carry the same text: a spec write syncs the rules from the body (WL-SPEC-77#sec-4g), and a rule edit regenerates the body (WL-REQ-1355) |
| `doc_versions` | `(doc_id, version)` primary key, `body`, `title`, `issued`, `created_at`. Append-only snapshot of every version `docs` has replaced. A body stored before headers left the store keeps its header verbatim |
| `doc_edge_versions` | `(doc_id, version)` plus the edge columns of `doc_edges` (`from_anchor`, `type`, the target columns): the document's outgoing edges at every replaced version. Append-only |
| `doc_rule_versions` | `(doc_id, version, position)` primary key, `rule_id`, `rule_version`, `depth`: the document's `doc_rules` at every replaced version, so version N shows the rule versions it arranged. Append-only |
| `doc_revisions` | the one open candidate revision per document: `doc_id` primary key, `body`, `created_by`, `created_at` (WL-SPEC-77#sec-9e) |
| `doc_revision_edges` | the candidate's proposed outgoing edge set: `doc_id` referencing `doc_revisions` and deleted with it, plus the edge columns of `doc_edges` |
| `doc_sections` | anchor, heading, depth, `last_revised_in`, `patched`. Specs only |
| `rules` | one design rule: project, `number` (the ref's number, from `project_entity_seq` kind `RULE`), `kind` (WL-SPEC-77#sec-4k), status (`draft`, `accepted`, `superseded`, `withdrawn`), current version, `owner`, `tags` |
| `rule_versions` | `(rule_id, version)` primary key, `heading`, `body`. A draft version is rewritten in place; an accepted version's text never changes |
| `rule_edge_versions` | `(rule_id, version)` plus `to_rule`, `type`, `source`: the rule's outgoing edges at every replaced version. Append-only |
| `doc_rules` | a spec's current arrangement of its rules: `(doc_id, position)` primary key, `rule_id`, `rule_version`, `depth`. Numbers and anchors are derived on read (WL-REQ-165). Written by the spec write path (WL-SPEC-77#sec-4g). A plan contains no rules and has no rows |
| `rule_edges` | `(from_rule, to_rule, type)` primary key, `source` (`manual`, `derived`, or `refactor` for an edge `lode rule supersede` wrote): the typed edges between rules, `refines`, `constrains`, `conflictsWith` and `references`, `amends`, and the lineage edges `supersedes` and `wasDerivedFrom` (WL-SPEC-77#sec-4i, WL-SPEC-77#sec-4m, WL-SPEC-77#sec-4n). Stored subject → object (WL-REQ-1288). `conflictsWith` is stored once per pair, unique on `(least(from_rule, to_rule), greatest(from_rule, to_rule), type)` |
| `task_governed_by` | `(task_id, rule_id)` primary key, `rule_version`, `source` (`plan`, `manual` or `gate`), `pinned_version` when the link is pinned: the rules governing a task (WL-REQ-1356, WL-SPEC-75#sec-4) |
| `doc_edges` | `covers` (from a plan to a rule), `defers`, `implements`, `requires`, `wasDerivedFrom`, `blockedBy`, section-scoped where an end is a section. A row is owned by its `from_doc` (WL-REQ-1288). Unresolvable targets stored verbatim in `to_external` |
| `tasks.plan_doc` | nullable, the plan whose declaration minted the task (WL-REQ-173) |
| `tasks.plan_task_key` | the declaration's title when the plan minted the task. Unique on `(plan_doc, plan_task_key)`, null together with `plan_doc` |
| `tasks.about_doc` | nullable, the document a review or design task is about |
| `decisions` | one row per question on a task (WL-SPEC-77#sec-12a) |

