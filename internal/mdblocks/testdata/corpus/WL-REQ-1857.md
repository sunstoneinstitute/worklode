
An inline reference is classified only when its node kind pair allows a
predicate other than `references`:

| Node → target | Candidate predicates |
|---|---|
| task → task | `dependsOn`, `childOf`, `followUpTo`, `duplicateOf` |
| rule → rule | `refines`, `needs`, `constrains`, `conflictsWith`, `amends`, `wasDerivedFrom` |
| doc → doc | `requires`, `wasDerivedFrom`, `blockedBy` (plan → plan only) |
| task → rule | `governedBy` |

`needs` here is the spec-time meaning in WL-REQ-1367: the rule cannot be
applied without a fact or term the target states. `supersedes` and `covers`
are never candidates, because each has one writer of its own (WL-REQ-1645,
WL-REQ-1647).

The `cue` classifier is deterministic and costs nothing to run, so it runs
first, on every derive. Its phrases are the `skos:prefLabel` and
`skos:altLabel` values of each candidate property in `ns/ontology.ttl` (for
example `wl:dependsOn` carries "depends on" and "blocked by"), generated into
`internal/ns` by `scripts/nsgen.py`, which refuses a phrase that labels two
properties, as it does for task kind words. A phrase on the reference's line that
names a candidate is a hit, and a hit is final. A phrase that names an inverse
property ("blocks <task id>") is not a hit, because the edge it describes
belongs to the target (WL-REQ-1288).

The `jev` classifier runs only when `--classify` is given and no cue matched.
It sends one TypeSafe Jev Choice question per reference, batched into one
`POST /v1/systemone` request per node. The question's state holds the node's
kind and title, the reference's line, its paragraph, and the target's kind
and title. Its criteria are the pair's candidates, each described by its
`skos:definition`, plus `references`, described as "refers to the target with
no stronger relation". The call never runs inside a store write transaction.
The API key is the server setting `LODE_TYPESAFE_API_KEY`, read from
`worklode-secrets`, and never leaves the server. Without it, `--classify`
fails with "classification not configured" and no other part of derive is
affected.
