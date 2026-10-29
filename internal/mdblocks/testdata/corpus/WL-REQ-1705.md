
Node shapes:

| Shape | Constraints |
|---|---|
| Task | exactly one `wl:taskKind`, one state literal, one `wl:inProject` |
| Component | at least one `wl:reviewer` |
| Deliverable | `dct:description`, at least one `dct:relation`, at least one `wl:deliveredBy` |
| Effect | Deliverable shape plus at least one `dct:relation` to a Deployment and zero to an Artifact |
| AcceptedDeviation | `rdf:subject`, `rdf:predicate`, `rdf:object`, `wl:sanctionedBy`, optional `dct:valid` |
| DesignDoc, Plan | exactly one `wl:status` from `wlc:DesignDocStatus` |
| Rule | `wl:status` from `wlc:DesignDocStatus`; `wl:lastRevisedIn` names a versioned DesignDoc; a superseded rule has a successor that `dct:replaces` it or a `dct:description` |
| Artifact | exactly one each of `wl:artifactKind`, `owl:versionInfo`, `dct:identifier` |
| Deployment | exactly one each of `wl:toEnvironment`, `wl:targetKind`, `wl:deploymentStatus`; at most one `prov:used` |
| Environment | `sh:in` over the `dev` and `prod` instances |
| Commit | exactly one `dct:identifier` |

`ns/shapes.ttl` also carries edge shapes for `wl:covers`, `wl:defers`, `wl:Deferral`, `wl:implements`, `wl:produces`, `wl:affects` and `wl:cutFrom`, and pins the Task state, priority and concern literals to their CHECK values. The Environment closure names the absolute IRIs `https://worklode.io/ns/id/environment/dev` and `.../prod`.

