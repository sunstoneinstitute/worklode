
Intent and execution classes:

| Class | Parent | Notes |
|---|---|---|
| `wl:Component` | none | atomic unit of the platform graph; a repo (`doap:Project`) holds many via `dct:hasPart` |
| `wl:DesignDoc` | `foaf:Document`, `prov:Entity` | `prov:Entity` is required because documents carry `prov:wasGeneratedBy`, `prov:wasRevisionOf`, `prov:wasAttributedTo` |
| `wl:ADR`, `wl:Spec` | `wl:DesignDoc` | disjoint |
| `wl:Section` | `foaf:Document` | addressable part of a DesignDoc; stable for the document's life, never deleted after acceptance |
| `wl:Plan` | `foaf:Document`, `prov:Entity` | reviewable and accept-gated, mutable and anchor-free; acceptance mints the execution subtree |
| `wl:Rule` | `wl:Section` | a design rule (WL-SPEC-77) |
| `wl:Skill` | none | a registry skill |
| `wl:Issue`, `wl:PullRequest` | `prov:Entity` | VCS objects; declared, not projected |
| `wl:Event` | `prov:Activity` | one row of the backbone event log |
| `wl:Task` | `prov:Activity` | projected; authorship is `prov:wasAssociatedWith` because Activity and Entity are disjoint in PROV |
| `wl:Deliverable` | none | declared definition-of-done |
| `wl:Effect` | `wl:Deliverable` | ships no artifact; target is a Deployment IRI; witness is a Commit |
| `wl:Project` | none | the backbone `projects` table verbatim; unbounded; owns 1..n repos; anchors projection named graphs |
| `wl:AcceptedDeviation` | none | sanctioned observed-but-unasserted edge (WL-REQ-244) |

Runtime classes, each tagged `wl:layer wlc:runtime`:

| Class | Parent | Meaning |
|---|---|---|
| `wl:Artifact` | `prov:Entity` | one built, versioned unit: container image, PyPI package, git tag, binary. Kind is `wl:artifactKind`, never a subclass, because the kinds carry identical edges |
| `wl:Build` | `prov:Activity` | the CI run that produced an Artifact; declared so `prov:wasGeneratedBy` is available; nothing produces one |
| `wl:Deployment` | `prov:Activity` | rollout of an Artifact or a Commit to one target in one Environment; one node per (environment, target kind, target name); current state only, no history |
| `wl:Environment` | none | a deployment stage; closed to `dev` and `prod` by SHACL |
| `wl:Commit` | `prov:Entity` | one commit on a repository's default branch; on the delivery state machine's critical path |
| `wl:RuntimeEvent` | `prov:Activity` | crashloop, OOM kill, Flux failure or recovery |

A git tag release is a `wl:Artifact` of kind `wlc:git_tag` with a `wl:cutFrom` edge to the commit its frontier reaches. There is no `wl:Release`.

