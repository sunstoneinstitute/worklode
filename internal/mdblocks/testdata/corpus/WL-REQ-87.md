
```sql
CREATE TABLE task_edges (
 from_task text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
 to_task text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
 type text NOT NULL CHECK (type IN
 ('child_of','blocks','follow_up_to','duplicate_of')),
 created_at timestamptz NOT NULL,
 UNIQUE (from_task, to_task, type)
);
CREATE UNIQUE INDEX task_edges_single_parent ON task_edges (from_task) WHERE type = 'child_of';
CREATE INDEX task_edges_children ON task_edges (to_task) WHERE type = 'child_of';
-- single origin and single canonical: same shape as task_edges_single_parent
CREATE UNIQUE INDEX task_edges_single_origin ON task_edges (from_task) WHERE type = 'follow_up_to';
CREATE UNIQUE INDEX task_edges_single_canonical ON task_edges (from_task) WHERE type = 'duplicate_of';
```

| Type | `A → B` means | Decides | Cardinality | Cross-project | Cycle check |
|---|---|---|---|---|---|
| `dependsOn` (stored as `B blocks A`) | A cannot be claimed while B is open (WL-RULE-86) | when a task may be claimed | many to many | yes | none needed |
| `child_of` | B is A's parent (WL-REQ-88) | what a task is made of | one parent per task | no | yes, `reachesViaChildOf` (BFS up the parent chain), `ErrCycle` |
| `follow_up_to` | A was spun out of work on B | nothing (provenance) | one origin per task | yes | none |
| `duplicate_of` | A and B are the same request, B is canonical | nothing (provenance) | one canonical per task | yes | none |

All types reject self-edges (`ErrInvalidInput`) and duplicates (`ErrEdgeExists`). `IsBlocked(tx, taskID)` runs inside the claim transaction. `follow_up_to` and `duplicate_of` gate nothing and grant no parenthood: a follow-up does not make its origin a container, and a duplicate stays claimable until closed by hand. Marking a duplicate changes nothing about the canonical task (no absorption of skills, edges, body or priority). A `dependsOn` edge may not end at a rally, and a `child_of` edge may not point into one.

The ordering relation is stored as the type `blocks`, from the prerequisite to the dependent: the row `B blocks A` means A depends on B. This spec writes that relation as `A dependsOn B`; the API, the CLI and the ranking queries read and write `blocks` rows. `child_of` is stored from child to parent, `follow_up_to` from the follow-up, `duplicate_of` from the duplicate.

