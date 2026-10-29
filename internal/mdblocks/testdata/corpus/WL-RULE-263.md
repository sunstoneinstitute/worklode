
- `wl:RuntimeEvent` has no natural key, so no deterministic IRI and no projection.
- Whether `wl:Build` should stay declared with no instances until workflow-run ingest exists.
- Whether the Environment closure to `{dev, prod}` survives Cluster and Namespace beneath it.
- How the per-environment frontier (`env_deploys`, keyed `(repo, environment)`) is modelled when a query needs it.
- Whether image-publish ingest is built, so that `wl:Artifact` covers more than `git_tag` and `prov:used` gets populated.
- Whether the graph should carry a Commit-side or Deployment-side frontier for Effects.
- Search: arm weights (`w = 1.0` untuned), a `pg_trgm` third arm for misspellings, chunk-level access control once project-scoped roles exist, and query rewriting for the agent path.

