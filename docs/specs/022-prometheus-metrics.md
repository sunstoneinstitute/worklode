---
status: draft
requires:
  - 004-execution-backbone.md
  - 011-delivery-lifecycle.md
  - 016-org-wide-skills.md
---
# Spec 022 — Prometheus domain metrics

## Purpose & scope

The server already carries the Prometheus scaffolding: a private registry with the Go
collector, `http_requests_total` / `http_request_duration_seconds` middleware with
mux-pattern route labels, `/metrics` on the admin listener (9090), and scrape annotations
in `deploy/base/deployment.yaml`. What it lacks is any signal about what the server
*does*: whether claims succeed, how many leases are live, whether the sweeper and skill
sync run clean, whether webhooks are being rejected, and how the outbound embedding API
behaves. This spec adds those domain metrics.

**In scope:** registry plumbing so packages outside `internal/api` can register
instruments; process and DB-pool collectors; lease/claim, sweeper, skill-sync, webhook,
and embedding metrics; tests; a `CLAUDE.md` maintenance rule so new server code keeps
its metrics current (§8).

**Out of scope:** OpenTelemetry or tracing, ServiceMonitor/PodMonitor CRs (annotation
discovery already scrapes 9090), renaming the existing `http_*` metrics, `/healthz`
changes, dashboards and alerts.

---

## 1. Registry plumbing {#sec-1}

The registry moves out of `api.NewServer` (`internal/api/server.go:305`) into
`internal/cmd/serve.go`, which becomes the composition root for metrics:

```go
reg := prometheus.NewRegistry()
reg.MustRegister(collectors.NewGoCollector())
reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
```

- `api.NewServer` takes the `*prometheus.Registry` (it both registers the HTTP
  middleware metrics and serves `promhttp.HandlerFor(reg, ...)` on the admin mux).
- Every other subsystem takes a `prometheus.Registerer` and owns its instruments in a
  package-private `metrics.go` next to the code they measure. No central metrics package.
- All metrics structs are nil-safe: a nil receiver no-ops, so the CLI and tests that
  don't pass a registerer pay nothing.

Every new metric is prefixed `worklode_`. The existing unprefixed `http_*` pair keeps
its names.

## 2. Process and DB-pool collectors {#sec-2}

`store.Open` gains a functional option `store.WithMetrics(reg prometheus.Registerer)`.
When present it registers:

- `collectors.NewDBStatsCollector(db, "worklode")` — pool gauges (open, in-use, waits).
  The pool is capped at 16 connections; wait counts are the saturation signal.
- The store's domain metrics (§3).

## 3. Lease and claim metrics (`internal/store`) {#sec-3}

| Metric | Type | Labels |
|---|---|---|
| `worklode_claims_total` | counter | `op` = `claim` \| `claim_next`, `outcome` |
| `worklode_lease_renewals_total` | counter | `outcome` = `ok` \| `error` |
| `worklode_lease_releases_total` | counter | `outcome` = `ok` \| `error` |
| `worklode_lease_expiries_total` | counter | — |
| `worklode_leases_active` | gauge (custom collector) | — |

`outcome` on claims maps from the store's sentinel errors: `ok`, `leased` (ErrLeased),
`blocked` (ErrBlocked), `not_found` (ErrNotFound), `none` (ClaimNext found no eligible
task), `error` (anything else). Incremented inside `Claim` (`leases.go:130`), `ClaimNext`
(`ranking.go:242`), `Renew`, `Release`. `ExpireLeases` (`leases.go:411`) adds the count
it expired.

`worklode_leases_active` is a small custom `prometheus.Collector` that counts unexpired
leases at scrape time with a 2-second timeout; on query failure it emits
`prometheus.NewInvalidMetric` rather than a stale zero. Scrapes hit the admin listener
only, so this is one trivial query per scrape interval.

## 4. Background jobs {#sec-4}

**Lease sweeper** (`internal/cmd/serve.go:101`): the 60s loop owns
`worklode_lease_sweeper_runs_total{result="ok"|"error"}`, registered directly in
`serve.go`. Expiry counts come from §3's `worklode_lease_expiries_total`.

**Skill sync** (`internal/api/server.go`, `runSkillSync`/`syncOnce`): instruments live on
the api server struct alongside the HTTP metrics.

| Metric | Type | Labels / buckets |
|---|---|---|
| `worklode_skill_sync_runs_total` | counter | `result` = `ok` \| `error` |
| `worklode_skill_sync_duration_seconds` | histogram | 0.1, 0.5, 1, 5, 15, 60, 300 |
| `worklode_skill_sync_items_total` | counter | `action` = `synced` \| `changed` \| `embedded` \| `deleted` |

Item counts come from the `skillsync.Summary` that `SyncAll` already returns; a partial
sync (summary plus error) records both its items and an `error` run.

## 5. Webhooks (`internal/hooks`) {#sec-5}

`worklode_webhook_events_total{source, event, result}` — one CounterVec shared by both
handlers via their constructors.

- `source`: `github` | `flux`.
- `event`: for GitHub, the `X-GitHub-Event` value if it is one the handler switches on
  (`issues`, `push`, `pull_request`, `pull_request_review`, `deployment_status`,
  `workflow_run`, `release`), else `other` — bounded cardinality. For Flux, `flux`.
- `result`: `ok` | `rejected` (signature/auth failure) | `ignored` (event type or action
  the handler drops) | `error`.

## 6. Embeddings (`internal/embed`) {#sec-6}

The `OpenAI` provider (`embed.go:104`) gains an optional registerer and wraps `Embed`:

| Metric | Type | Labels / buckets |
|---|---|---|
| `worklode_embed_requests_total` | counter | `result` = `ok` \| `error` |
| `worklode_embed_request_duration_seconds` | histogram | 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10 |

One observation per `Embed` call (which may batch many texts), not per text.

## 7. Testing {#sec-7}

- Per-package unit tests pass a fresh `prometheus.NewRegistry()` and assert with
  `prometheus/testutil` (`ToFloat64`, `CollectAndCount`) after driving the operation —
  e.g. a failed claim increments `{op="claim",outcome="leased"}`.
- `TestMetricsEndpoint` (`internal/api/server_test.go:230`) extends to assert the new
  families appear in the admin `/metrics` body.
- `TestHealthzAndMetricsNotOnPublicHandler` stays green: nothing new lands on the
  public mux.
- `newTestServer`/`newTestServerAdmin` construct the registry themselves, mirroring
  `serve.go`, so the `NewServer` signature change is absorbed in the helpers.

## 8. Maintenance instructions {#sec-8}

Metrics rot when new server code ships without them. The top-level `CLAUDE.md` gains a
short **Metrics** section stating the rule and pointing here for details:

- Server-side changes that add an HTTP endpoint, background loop, outbound call, or
  store operation with meaningful outcomes must add or extend `worklode_*` metrics in
  the owning package, following this spec's conventions (package-private nil-safe
  metrics struct, `prometheus.Registerer` threading from `serve.go`, bounded label
  values, `worklode_` prefix).
- Metric changes ship with the `testutil` assertions and the `TestMetricsEndpoint`
  family check described in §7.

The paragraph stays short — the conventions live in this spec, not in `CLAUDE.md`.
