
| Property | Domain | Range | Characteristics |
|---|---|---|---|
| `wl:governs` | DesignDoc | Component | declared intent |
| `wl:deliveredBy` | Deliverable | Component | declared; at least one (SHACL); deliberately not functional |
| `wl:covers` | Plan | Section | planning intent; the plan builds the whole section, so the edge carries no level (WL-SPEC-78#sec-4.6) |
| `wl:defers` | Plan | Section | handoff to the owner named on the qualified `wl:Deferral` node |
| `wl:implements` | Component | Section | evidence only; derived by `observed/repo-implements` from `.worklode/implements.yaml`; never declared on a plan or a task |
| `wl:pinnedVersion` | the reifier of a `wl:implements` edge (`rdf:reifies` a triple term; the reifier is an IRI keyed on the edge) | DesignDoc version snapshot | functional; declared in RDF 1.1, used with RDF 1.2 data; stale-claim query compares `dcat:version` numbers, never IRI strings |
| `wl:produces` | Task | Deliverable | the task that makes the deliverable exist |
| `wl:affects` | (Task, Issue, PullRequest) | Component | observed |
| `wl:status` | DesignDoc, Plan, Section (union) | `skos:Concept` in `wlc:DesignDocStatus` | functional; SHACL requires one |
| `wl:humanOnly` | Task | `xsd:boolean` | functional; the task is reserved for a human and no agent may claim it; emitted only as `true`, absence means false |
| `wl:followUpTo` | Task | Task | functional |
| `wl:duplicateOf` | Task | Task | points at the canonical task |
| `wl:sanctionedBy` | AcceptedDeviation | DesignDoc | the authorising ADR |
| `wl:taskKind` | Task | `wlc:TaskKind` concept | functional |
| `wl:priority` | Task | `xsd:string` | functional literal mirror of the backbone enum (critical/high/medium/low) |
| `wl:concern` | Task | `xsd:string` | literal mirror; absent when none |
| `wl:taskState` | Task | literal | mirror of `tasks.state`; legal values are the column's CHECK; transitions are not modelled |
| `wl:dependsOn` | Task | Task | transitive; `rdfs:subPropertyOf dct:requires` |
| `wl:blocks` | Task | Task | transitive; `owl:inverseOf wl:dependsOn` |
| `wl:inProject` | Task | Project | functional; from `tasks.project_id` |
| `wl:reviewer` | Component | `foaf:Agent` | notify on PRs |
| `wl:mirrors` | Task or Issue | Task or Issue | symmetric; domain and range are the same union so no Issue is entailed to be a Task; SHACL pins the pairing |
| `wl:layer` | any term | `wlc:ModelLayer` concept | annotation property |
| `wl:artifactKind` | Artifact | `wlc:ArtifactKind` | functional |
| `wl:digest` | Artifact | `xsd:string` | opaque prefixed digest such as `sha256:...` |
| `wl:toEnvironment` | Deployment | Environment | functional |
| `wl:deploymentStatus` | Deployment | `wlc:DeploymentStatus` | functional |
| `wl:targetKind` | Deployment | `wlc:DeployTargetKind` | functional |
| `wl:runtimeEventKind` | RuntimeEvent | `wlc:RuntimeEventKind` | functional |
| `wl:cutFrom` | Artifact | Commit | the commit a published release was cut from |

The three section-facing predicates split by domain: a Plan `covers` a section, a Component `implements` it, a Task `produces` a Deliverable. None of the three is asserted from another domain. Task execution state is never `wl:status`. The backbone owns the state machine (WL-SPEC-75); the graph mirrors it as a literal.

