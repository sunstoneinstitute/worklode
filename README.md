# work-tracker

## What it is

work-tracker is Sunstone Institute's org-wide work tracker: one authoritative
view of planned and in-progress work across all repos, replacing a
hand-maintained `TASKS.md` + GitHub issue sync. It ships as a single Go
binary, `wt`, backed by a SQLite database, with an append-only event log
giving full provenance for every state change. Work arrives from three
sources — a GitHub App (issues, PRs, reviews, CI, releases), a Flux
notification-controller webhook (deployments), and a Kubernetes pod watcher
(crash loops, OOM kills) — and is read back through the `wt` CLI or a
read-only web UI. See `docs/spec.md` for the full design.

## Quickstart

Start the server with Docker Compose. `WT_BOOTSTRAP_TOKEN` creates the
initial admin actor the first time the database is empty — pick any
non-guessable string:

```bash
mkdir -p data
export WT_BOOTSTRAP_TOKEN=wt_$(openssl rand -hex 20)
docker compose up -d
```

Point the CLI at it, either via `~/.config/wt/config.toml`:

```toml
server = "http://localhost:8080"
token = "wt_..."   # the WT_BOOTSTRAP_TOKEN value
```

or via environment variables:

```bash
export WT_SERVER=http://localhost:8080
export WT_TOKEN=$WT_BOOTSTRAP_TOKEN
```

Then create a project, map a repo to it, add a task, and claim it:

```bash
wt project add sunstone-web --name "Sunstone Web"
wt project add-repo sunstone-web sunstoneinstitute/sunstone-web
wt task add --project sunstone-web --title "Fix the footer link"
wt task claim <task-id>
```

The read-only web UI is at http://localhost:8080/.

## GitHub App setup

Create a GitHub App on the `sunstoneinstitute` org and install it org-wide
(all repos). Configure:

- **Webhook URL**: `https://<host>/hooks/github`
- **Webhook secret**: a random string, set as `WT_GITHUB_WEBHOOK_SECRET` on
  the server
- **Subscribe to events**: Issues, Pull requests, Pull request reviews,
  Workflow runs, Releases

For local testing without a public URL, forward deliveries from a real repo:

```bash
gh webhook forward --repo=sunstoneinstitute/<repo> \
  --events=issues,pull_request,pull_request_review,workflow_run,release \
  --url=http://localhost:8080/hooks/github
```

or use a [smee.io](https://smee.io) relay as the App's webhook URL and pipe
it to the same local endpoint.

## Flux setup

Point Flux's notification-controller at `/hooks/flux` with a
`generic-hmac` Provider and an Alert watching your Kustomizations/HelmReleases:

```yaml
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Provider
metadata:
  name: work-tracker
  namespace: flux-system
spec:
  type: generic-hmac
  address: https://<host>/hooks/flux
  secretRef:
    name: work-tracker-hmac
---
apiVersion: v1
kind: Secret
metadata:
  name: work-tracker-hmac
  namespace: flux-system
stringData:
  hmac-key: <same value as WT_FLUX_WEBHOOK_SECRET>
---
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Alert
metadata:
  name: work-tracker
  namespace: flux-system
spec:
  providerRef:
    name: work-tracker
  eventSeverity: info
  eventSources:
    - kind: Kustomization
      name: "*"
    - kind: HelmRelease
      name: "*"
```

Set `WT_FLUX_WEBHOOK_SECRET` on the server to the same HMAC key, and
`WT_CLUSTER_ENV_MAP` to map cluster names to environments, e.g.
`WT_CLUSTER_ENV_MAP="prod-cluster=prod,staging-cluster=staging"`. A cluster
missing from the map falls back to the `dev` environment.

## Cluster watcher

`wt watch` runs a pod informer against one cluster and reports crash loops
and OOM kills to the server:

```bash
wt watch --kubeconfig ~/.kube/config --cluster dev \
  --server http://localhost:8080 --token $WT_TOKEN
```

`--server`/`--token` default to the `WT_SERVER`/`WT_TOKEN` environment
variables, same as the CLI client. Omit `--kubeconfig` when running in-cluster
(it falls back to the in-cluster config).

## Backups

The `backup` Compose profile runs [litestream](https://litestream.io) to
continuously replicate `data/wt.db` to S3-compatible object storage (Hetzner
Object Storage, or any other S3-compatible provider — see the comments in
`litestream.yml`):

```bash
export LITESTREAM_BUCKET=sunstone-wt-backups
export LITESTREAM_ENDPOINT=https://fsn1.your-objectstorage.com
export LITESTREAM_ACCESS_KEY_ID=...
export LITESTREAM_SECRET_ACCESS_KEY=...
docker compose --profile backup up -d
```

## Development

Requires Go 1.25. Run the test suite with:

```bash
go test ./...
```

Migrations live under `internal/store/migrations/` and use
[golang-migrate](https://github.com/golang-migrate/migrate), embedded into
the binary and applied automatically on `wt serve`/`wt migrate` startup.
Never edit a migration that has already shipped — add a new pair instead:

```
NNNN_name.up.sql
NNNN_name.down.sql
```

where `NNNN` is the next sequence number.
