
Reused runtime terms and their source columns:

| Fact | Term | Column |
|---|---|---|
| built by | `prov:wasGeneratedBy` to `wl:Build` | none |
| built from | `prov:wasDerivedFrom` to `wl:Commit` | `artifacts.source_sha` |
| build time | `prov:generatedAtTime` on the Artifact | `artifacts.built_at` |
| deployment uses | `prov:used` | `deployments.artifact_id` |
| event affects | `prov:used` | `runtime_events.artifact_id` |
| first seen | `prov:startedAtTime` | `deployments.first_seen` |
| last updated | `dct:modified` | `deployments.last_update` |
| event or publication time | `dct:date` | `runtime_events.occurred_at`, `github_release_frontiers.published_at` |
| version | `owl:versionInfo` | `artifacts.version` |
| name, target, sha | `dct:identifier` | the coordinate in the IRI |
| label | `dct:title` | |
