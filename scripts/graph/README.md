# Querying the task graph

`lode graph triples` writes the task graph as N-Triples. Loaded into an RDF
store alongside `ns/ontology.ttl`, it answers questions the fixed frontier
ranking cannot: which work unlocks the most, which gating decisions are worth
resolving first, what is waiting on a person.

The ontology already declares `wl:blocks` as an `owl:TransitiveProperty` and
`owl:inverseOf wl:dependsOn`. A reasoner therefore derives the whole blocking
closure, and the `wl:dependsOn` direction, with no rules written here.

## Running

Needs [HornDB](https://github.com/sunstoneinstitute/horndb)'s `serve` on PATH
(or `HORNDB=/path/to/serve`).

```sh
make build                       # scripts/graph/serve.sh calls bin/lode
scripts/graph/serve.sh           # export, then serve on :3840 and :3841
scripts/graph/sparql.py 3840 scripts/graph/queries/gates.rq
```

Or via make:

```sh
make graph-serve
make graph-query Q=gates
```

## The two endpoints

| port | mode | use for |
|---|---|---|
| 3840 | raw | anything that must tell a **direct** edge from a derived one |
| 3841 | `--materialize` | OWL 2 RL closure: `prp-trp` closes `wl:blocks`, `prp-inv1/2` derives `wl:dependsOn` |

Both are needed because materialization erases the distinction between an
asserted and an inferred edge, and worklode's own "is it blocked" rule
(`blockedCondition`, `internal/store/tasks.go`) looks only at direct edges: a
task whose direct blocker is merged is startable even when something further up
the chain is open.

Fan-out answers agree across the two, and agree with `store.BlockingFanOut`
(`lode task frontier`). Three independent implementations is a useful check
that an export is faithful.

## Notes

- **Closed states** are hardcoded in the queries as `abandoned, merged,
  deployed_dev, deployed_prod, released`. `store.taskClosed` is really per repo
  (state rank at or past that repo's `done_state`, default `merged`), which a
  state-only projection cannot express. The literal list is exact while every
  repo sits at `done_state = merged`; revisit it if one moves to `released`.
- **HornDB 0.6.0 implements neither `FILTER NOT EXISTS` nor `MINUS`.** Negation
  is written as `OPTIONAL { ... } FILTER(!BOUND(?x))`.
- Both servers are in-memory and load once. Re-exporting means restarting them.
