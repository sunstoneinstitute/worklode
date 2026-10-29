
Standards-first. A community term is reused wherever it carries the intended meaning. `wl:` terms are minted sparingly. No gtio ontology is used: `gtio-sc:Component` is a supply-chain term.

| Concept | Term | Decision |
|---|---|---|
| Dependency | `dct:requires` | reuse |
| Decomposition | `dct:hasPart` / `dct:isPartOf` | reuse |
| Supersession, whole or section | `dct:replaces` / `dct:isReplacedBy` between Section nodes | reuse |
| Owner, author | `foaf:Agent`, `prov:Agent`, `prov:wasAttributedTo` | reuse |
| Provenance | `prov:wasGeneratedBy`, `prov:wasDerivedFrom`, `prov:Activity` | reuse |
| Repository grouping | `doap:Project` (`doap:repository`, `doap:Version`) | reuse |
| Enums | `skos:Concept`, `skos:ConceptScheme`, `skos:inScheme` | reuse |
| Titles, descriptions, dates | `dct:title`, `dct:description`, `dct:created`, `dct:modified` | reuse |
| Documents | `foaf:Document` | reuse |
| Software component | `wl:Component` | mint |
| Design document | `wl:DesignDoc` with subclasses `wl:Spec` and `wl:ADR`. New documents are specs; `wl:ADR` types the ADRs that exist (WL-SPEC-77) | mint |
| Document section | `wl:Section`, `wl:lastRevisedIn` | mint |
| Design rule | `wl:Rule`, subclass of `wl:Section`: the lowest heading unit of a spec or ADR, with its own identity, status and versions; `wl:ruleKind` from `wlc:RuleKind`. WL-SPEC-77 defines the rule model | mint |
| Rule to rule | `wl:refines`, `wl:constrains`, `wl:conflictsWith` (symmetric), `wl:amends`, written by an architect; `wl:references`, derived from the rule text on every version | mint |
| Plan | `wl:Plan`, sibling of DesignDoc | mint |
| Task | `wl:Task` | mint, projected |
| Deliverable, Effect | `wl:Deliverable`, `wl:Effect` subclass | mint |
| Project umbrella | `wl:Project` | mint, named-graph anchor |
| Skill and document pin | `wl:Skill`, `wl:recommendsSkill` | mint |
| Issue, pull request | `wl:Issue`, `wl:PullRequest`, both `prov:Entity` | mint |
| Design to component | `wl:governs` | mint |
| Deliverable to component | `wl:deliveredBy` | mint |
| Plan to section undertaking | `wl:covers`, unqualified: the plan builds the whole section | mint |
| Plan to section handoff | `wl:defers`, qualified by `wl:Deferral` (`wl:deferringPlan`, `wl:deferredSection`, `wl:deferredTo`) | mint |
| Plan ordering | `wl:blockedByPlan` / `wl:blocksPlan` | mint |
| Task to document | `wl:plannedIn` (the plan that minted the task), `wl:about` (the document a review or planning task is about) | mint |
| Task to skill | `wl:requiresSkill` | mint |
| Component to section evidence | `wl:implements`, annotated with `wl:pinnedVersion` | mint |
| Task to deliverable | `wl:produces` | mint |
| Work to component | `wl:affects` | mint |
| Lifecycle status | `wl:status` with `wlc:DesignDocStatus` | mint |
| Task kind | `wl:taskKind` with `wlc:TaskKind` | mint |
| Task state, priority, concern mirrors | `wl:taskState`, `wl:priority`, `wl:concern` literals | mint |
| Task dependency | `wl:dependsOn` / `wl:blocks`, transitive | mint |
| Task membership | `wl:inProject`, exactly one | mint |
| Follow-up, duplicate | `wl:followUpTo`, `wl:duplicateOf` | mint |
| Human-only flag | `wl:humanOnly` | mint |
| Component reviewer | `wl:reviewer` to `foaf:Agent` | mint |
| Task and issue mirror | `wl:mirrors`, symmetric | mint |
| Model-layer tag | `wl:layer` with `wlc:ModelLayer` | mint |
| Accepted deviation | `wl:AcceptedDeviation`, `wl:sanctionedBy`, RDF reification | mint |
| Release to commit | `wl:cutFrom` | mint |
| Runtime layer | `wl:Artifact`, `wl:Build`, `wl:Deployment`, `wl:Environment`, `wl:Commit`, `wl:RuntimeEvent`, seven properties, four schemes | mint |
| Backbone event | `wl:Event` (subclass `prov:Activity`) with `wl:DocumentSubmitted`, `wl:DocumentAccepted`, `wl:subject`, `wl:fromStatus`, `wl:toStatus` | mint |
| Declared evidence address | `wl:artifact` (string, no domain) | mint |
| Component coverage gap | `wl:unmatchedPath` on the repository node | mint |
| Task-kind behavior | `wl:claimable`, `wl:canBlock`, `wl:allowsChildren`, `wl:allowsDecisions`, `wl:closesOnAnswers`, `wl:kindMutable`, `wl:planMintable` on `wlc:TaskKind` concepts | mint |
| Edge bookkeeping | `wl:edgeOrigin` (`wlc:EdgeOrigin`: `declared`, `inferred`) and `wl:storedAs` (the backbone table and type), annotation properties | mint |

`wl:Milestone`, `wl:Release`, `wl:Coverage` and SKOS schemes for backbone-owned enums (task state, priority, concern) are not minted; `ns/ontology.ttl` lists each with its reason. The runtime layer was surveyed against PROV-O, SPDX 3.0.1, DOAP, DCAT v3, schema.org, OSLC Automation, `sd:`, SLSA/in-toto, CycloneDX and TOSCA. PROV-O is the anchor; the rest offer no truthful parent. SPDX and `sd:` are cited with `rdfs:seeAlso` and never imported. The only external terms in use are `prov:`, `dcterms:`, `doap:Project`, `skos:` and `owl:versionInfo`.

