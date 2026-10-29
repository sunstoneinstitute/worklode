
The local part is the singular type followed by the natural key, one path segment per key column. Slashes inside a key are legal. Every IRI is branch-free.

| Type | Local part under `wlid:` | Example |
|---|---|---|
| Task | `task/<task-id>` | `task/WL-42` |
| Project | `project/<project-id>` | `project/worklode` |
| Agent | `agent/<actor-id>` | `agent/stig` |
| Component | `component/<slug>` | `component/github.com/sunstoneinstitute/worklode` |
| Document | `doc/<doc-key>` | `doc/spec-worklode-079` |
| Document version | `doc/<doc-key>/v<n>` | `doc/spec-worklode-079/v3` |
| Rule | `rule/<project-id>/<number>` | `rule/worklode/165` |
| Implements claim (reifier) | `claim/<component local id>/<rule local id>` | |
| Deliverable | `deliverable/<id>` | `deliverable/worklode-graph-live` |
| Issue | `issue/<host>/<owner>/<repo>/<number>` | `issue/github.com/sunstoneinstitute/worklode/42` |
| Pull request | `pr/<host>/<owner>/<repo>/<number>` | `pr/github.com/sunstoneinstitute/worklode/42` |
| Repository | `repo/<host>/<owner>/<name>` | `repo/github.com/sunstoneinstitute/worklode` |
| Commit | `commit/<host>/<owner>/<repo>/<sha>` | `commit/github.com/sunstoneinstitute/worklode/a16c2a7` |
| Artifact | `artifact/<kind>/<name>/<version>` | `artifact/pypi/sunstone-py/0.4.1` |
| Deployment | `deployment/<env>/<target-kind>/<target-name>` | `deployment/prod/flux_kustomization/graph-server` |
| Environment | `environment/<name>` | `environment/prod` |

A document's key is project-qualified: `<kind>-<project>-<nnn>`, the number zero-padded to three digits, because document numbers are unique only within a project. A document has exactly this one IRI. A rule's IRI is stable because its ref never changes; the number is the one in its ref (WL-REQ-1642). A section has no IRI and the projection emits no section node: section numbers and anchors are derived from a spec's arrangement (WL-REQ-165), and what a section holds is the rule arranged there. Versions are ordered by `dcat:version`, never by IRI. The Artifact grammar is kind-first, matching `UNIQUE (kind, name, version)`. `wl:Build` and `wl:RuntimeEvent` have no IRI.

