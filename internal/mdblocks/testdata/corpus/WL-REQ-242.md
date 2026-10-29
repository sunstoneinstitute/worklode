
Layer 1, intent, declared and authored in the graph:

| Node | Class | Status |
|---|---|---|
| Component | `wl:Component` | declared in each repository's `.worklode/components.yaml` |
| DesignDoc | `wl:ADR` / `wl:Spec`, addressable by `wl:Section` | authored in the backbone, projected |
| Deliverable, Effect | `wl:Deliverable`, `wl:Effect` | vocabulary and shapes only; nothing authors one (§8) |

Intent edges: `wl:governs`, `wl:reviewer`, `wl:deliveredBy`, `dct:hasPart`, `dct:requires`, `dct:replaces`.

Layer 2, execution and VCS, observed:

| Node | Class | Status |
|---|---|---|
| Task | `wl:Task` | projected from the backbone |
| Project | `wl:Project` | projected, named-graph anchor |
| Plan | `wl:Plan` | authored in the backbone, projected |
| Issue, PullRequest | `wl:Issue`, `wl:PullRequest` | declared, not projected |

Execution edges: `wl:produces`, `wl:affects`, `wl:taskKind`, `wl:mirrors`, `wl:inProject`, `wl:dependsOn` / `wl:blocks`, `wl:followUpTo`, `wl:duplicateOf`, `dct:isPartOf` (`child_of`), `prov:wasAttributedTo` from Issue or PullRequest, `prov:wasAssociatedWith` from Task. §12 says which of them are projected.

Layer 3, runtime and deploy, is the six runtime classes of section 3. The layers join vertically at Deliverable.

