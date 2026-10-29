
| Entity or edge | Layer | Source | Projected into |
|---|---|---|---|
| Task node with `wl:concern`, `wl:priority`, `wl:taskState`, `wl:taskKind`, `wl:humanOnly` (only when true), `dct:title`/`created`/`modified`, `prov:wasAssociatedWith` | 2 | `tasks` | `project/<id>` |
| `dct:isPartOf` (`child_of`), `wl:blocks` / `wl:dependsOn`, `wl:followUpTo`, `wl:duplicateOf` | 2 | `task_edges` | `project/<id>` |
| `wl:inProject` and Project node | 2 | `projects` | `project/<id>` |
| DesignDoc, Plan, Rule, version nodes and document edges | 1 | `docs`, `doc_rules`, `rules`, `doc_edges` | `declared/<doc-key>`, `declared/<doc-key>/v<n>` |
| `wl:affects` (Task to Component) | 2 | PR changed paths matched against `components.yaml` | `observed/pr-affects` |
| Artifact, Deployment, Environment, Commit, `wl:cutFrom` | 3 | `artifacts`, `deployments`, `main_commits`, `github_release_frontiers` | `observed/deploy` |
| `wl:implements` | 1 | `.worklode/implements.yaml`, read by `lode graph derive` in the repository | `observed/repo-implements/<host>/<owner>/<name>` |
| Component, `governs`, `reviewer`, Deliverable | 1 | authored outside Worklode | none |

`wl:produces`, `wl:mirrors`, Issue and PullRequest are not projected: the backbone stores no `produces` relation, and projecting a predicate with no source would fabricate facts. The `pr-affects` and `deploy` derivers run on `POST /api/v1/derive` (admin) and replace their observed graph whole; they refuse to replace a graph with an empty document.

Runtime triples in `observed/deploy`:

| Node or edge | Source |
|---|---|
| `wl:Artifact` with kind, version, digest | `artifacts` |
| `prov:wasDerivedFrom` to `wl:Commit` | `artifacts.source_sha`, only when it resolves |
| `wl:Deployment` with status, target kind, environment | `deployments` |
| `prov:used` (Deployment to Artifact) | `deployments.artifact_id`, when set |
| `wl:Environment` | the fixed set `dev`, `prod` |
| `wl:Commit` | `main_commits` |
| `wl:cutFrom` | `github_release_frontiers` |

Only `git_tag` artifacts are created (`applyRelease` in `internal/hooks/github.go`), so `deployments.artifact_id` stays null and `prov:used` is not emitted in practice. A branch name in `target_commitish` projects no commit edge. `env_deploys` is keyed `(repo, environment)`, which matches no node, so the per-environment frontier has no node. `runtime_events` and builds are not projected.
