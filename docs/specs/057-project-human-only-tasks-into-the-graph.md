---
status: draft
issued: 2026-09-02
kind: adr
requires:
- 006-knowledge-graph.md
amends:
  "#sec-2":
  - 006-knowledge-graph.md#sec-11
---
# ADR 057 — Project the human-only flag into the graph as `wl:humanOnly`

## 0. Decision {#sec-0}

`wl:humanOnly` joins the v1 backbone→graph projection as a Task property. It
mirrors the backbone `tasks.human_only` column (migration
`0060_task_human_only`), which means the task is reserved for a human and no
agent may claim it.

The projector emits the triple **only when the flag is true**, as
`"true"^^xsd:boolean`. A task with the flag off carries no `wl:humanOnly`
triple at all. Absent means false.

## 1. Why {#sec-1}

The graph is where "what is blocking the most work" gets asked, because
`wl:blocks`/`wl:dependsOn` are transitive and reachability is one SPARQL
property path. Human-gated tasks are the interesting case of that question: a
task no agent may claim only moves when a person decides something, so its
dependants wait on a person, not on capacity.

No projected term answers this today. `wl:taskKind` says a task is a spike, a
design or a review, which is about the shape of the work, not about who may
pick it up. A review task can be agent-run and a feature task can be reserved
for a human, so the two are independent and neither derives the other.
`wl:taskState` does not carry it either: a human-only task sits in `ready`
alongside every other pickable task.

Because the flag lives in the backbone and nothing projected it, the graph
answered "which tasks unlock the most work" without being able to answer
"which of those wait on a person". One extra triple closes that gap.

## 2. Amendment to 006 §11 {#sec-2}

Spec 006 §11 holds the table of what the projector emits. Add this row, after
the Task node row:

| Entity / edge | Layer | Authority | v1? | Projected? | Trigger |
|---|---|---|---|---|---|
| `wl:humanOnly` (Task, emitted only when true) | 2 | backbone | v1 | yes | task lifecycle event (create/edit) |

Everything else in §11 is unchanged.

## 3. Scope {#sec-3}

One extra triple per human-only task. No change to how the projector works, no
schema change, no new backbone state: the column already exists and the
projector already reads the task row it sits on.

Emitting nothing for false is deliberate. Most tasks are not human-only, so
projecting `"false"^^xsd:boolean` on every one of them would grow the graph
for no query that could not be written as `FILTER NOT EXISTS`. It also matches
how the projection already treats `wl:concern`: an absent optional, not a
present empty one. The cost is that a consumer must read absence as false
rather than as unknown, which is what the ontology comment and the
`wl:TaskShape` message both say.

`ns/ontology.ttl` declares the term (`owl:DatatypeProperty`,
`owl:FunctionalProperty`, domain `wl:Task`, range `xsd:boolean`) and
`ns/shapes.ttl` constrains it on `wl:TaskShape` with `sh:maxCount 1`.
