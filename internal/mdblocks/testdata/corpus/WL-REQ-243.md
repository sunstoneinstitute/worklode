
A Deliverable is the declared target where a spec's intent meets a running system. The vocabulary and shapes define it; no backbone row or authoring surface produces one yet, and the backbone `deliverables` table is a different entity. A Deliverable names the Component that delivers it and typed `dct:relation` targets.

| | Declares | Confirmed when |
|---|---|---|
| `wl:Deliverable` | Artifact plus Environment | a Deployment `prov:used` that Artifact reaches `wlc:deployed` |
| `wl:Effect` | Deployment target, no Artifact | that Deployment `prov:used` a Commit on the delivering component's default branch and reaches `wlc:deployed` |

An Effect's target IRI is stable (environment, target kind, target name). A Commit IRI carries a SHA and cannot be named in advance, so the commit is discovered, never declared. A Spec scopes a deliverable through `dct:hasPart`; a Task realises it through `wl:produces`. Nothing confirms a Deliverable automatically. An Effect has no graphed frontier; `env_deploys.main_seq` stays authoritative relationally.

