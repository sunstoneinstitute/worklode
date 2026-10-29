
These are the store, skill-sync, webhook and embedding metric families. Every other family is declared in its owning package's `metrics.go` under the conventions of WL-REQ-32.

| Metric | Type | Labels or buckets | Owner |
|---|---|---|---|
| `worklode_claims_total` | counter | `op` = `claim`, `claim_next`; `outcome` = `ok`, `leased`, `blocked`, `not_found`, `none`, `error` | `internal/store` |
| `worklode_lease_renewals_total` | counter | `outcome` = `ok`, `error` | `internal/store` |
| `worklode_lease_releases_total` | counter | `outcome` = `ok`, `error` | `internal/store` |
| `worklode_lease_expiries_total` | counter | none; `ExpireLeases` adds its count | `internal/store` |
| `worklode_leases_active` | gauge (custom collector) | none; counts unexpired leases at scrape with a 2 s timeout, emits `NewInvalidMetric` on failure | `internal/store` |
| `worklode_lease_sweeper_runs_total` | counter | `result` = `ok`, `error`; 60 s loop | `internal/store` |
| `worklode_skill_sync_runs_total` | counter | `result` = `ok`, `error` | `internal/api` |
| `worklode_skill_sync_duration_seconds` | histogram | 0.1, 0.5, 1, 5, 15, 60, 300 | `internal/api` |
| `worklode_skill_sync_items_total` | counter | `action` = `synced`, `changed`, `embedded`, `deleted`; from `skillsync.Summary` | `internal/api` |
| `worklode_webhook_events_total` | counter | `source` = `github`, `flux`, or an artifact-evidence ingest's source such as `catalog`; `event`; `result` = `ok`, `rejected`, `ignored`, `unrouted`, `error` | `internal/hooks` |
| `worklode_artifact_evidence_total` | counter | `source` (the ingest's configured source, such as `catalog`); `state`; `entity_kind` = `deliverable`, `task`, `doc`; counted after commit | `internal/hooks` |
| `worklode_embed_requests_total` | counter | `result` = `ok`, `error`; one per `Embed` call | `internal/embed` |
| `worklode_embed_request_duration_seconds` | histogram | 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10 | `internal/embed` |

Webhook `event` values: for GitHub, the `X-GitHub-Event` value when the handler switches on it (`issues`, `push`, `pull_request`, `pull_request_review`, `deployment_status`, `workflow_run`, `release`), else `other`. For Flux, `flux`. For the data catalog, the artifact state `published`, `updated`, `deprecated`, `removed`, `failed`, or `invalid` when rejected before a state could be read. `unrouted` is catalog-only: the delivery was authentic and stored but no open entity had declared the artifact. A partial skill sync records its items and an `error` run.

