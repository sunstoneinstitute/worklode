# work-tracker

## What it is

work-tracker is Sunstone Institute's org-wide work tracker: one authoritative
view of planned and in-progress work across all repos, replacing a
hand-maintained `TASKS.md` + GitHub issue sync. It ships as a single Go
binary, `wl`, backed by a SQLite database, with an append-only event log
giving full provenance for every state change. Work arrives from three
sources — a GitHub App (issues, PRs, reviews, CI, releases), a Flux
notification-controller webhook (deployments), and a Kubernetes pod watcher
(crash loops, OOM kills) — and is read back through the `wl` CLI or a
read-only web UI. See `docs/spec.md` for the full design.

## Quickstart

Start the server with Docker Compose. `WL_BOOTSTRAP_TOKEN` creates the
initial admin actor the first time the database is empty. It must match
`^wl_[0-9a-f]{40}$` — the exact form `wl_$(openssl rand -hex 20)` mints.
A bare `openssl rand -hex 20` (no `wl_` prefix) fails validation at startup:

```bash
mkdir -p data
export WL_BOOTSTRAP_TOKEN=wl_$(openssl rand -hex 20)
docker compose up -d
```

On native Linux Docker (not Docker Desktop) the container runs as uid 65532,
so run `sudo chown 65532:65532 data` (or use a named volume) before first
start.

Install the `wl` CLI:

```bash
go install ./cmd/wl    # or: go build -o ~/bin/wl ./cmd/wl
```

Point the CLI at it, either via `~/.config/worklode/config.toml`:

```toml
server = "http://localhost:8080"
token = "wl_..."   # the WL_BOOTSTRAP_TOKEN value
```

or via environment variables:

```bash
export WL_SERVER=http://localhost:8080
export WL_TOKEN=$WL_BOOTSTRAP_TOKEN
```

Then create a project, map a repo to it, add a task, and claim it:

```bash
wl project add sunstone-web --name "Sunstone Web"
wl project add-repo sunstone-web sunstoneinstitute/sunstone-web
wl task add --project sunstone-web --title "Fix the footer link"
wl task claim <task-id>
```

Managing projects, actors, and tokens requires an admin actor; the
bootstrap actor is admin, and `wl actor add --admin` creates more.

The read-only web UI is at http://localhost:8080/.

## Network exposure

The compose file publishes port 8080 on loopback only
(`"127.0.0.1:8080:8080"`). Keep it that way on shared hosts: the web board
and `/metrics` are unauthenticated (bearer tokens cover `/api/v1` only), so
anyone who can reach the port can read every task. To serve other machines,
change the mapping to `"8080:8080"` (or a specific interface) and put a
TLS-terminating reverse proxy or firewall in front.

## GitHub App setup

Create a GitHub App on the `sunstoneinstitute` org and install it org-wide
(all repos). Configure:

- **Webhook URL**: `https://<host>/hooks/github`
- **Webhook secret**: a random string, set as `WL_GITHUB_WEBHOOK_SECRET` on
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
  hmac-key: <same value as WL_FLUX_WEBHOOK_SECRET>
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

Set `WL_FLUX_WEBHOOK_SECRET` on the server to the same HMAC key, and
`WL_CLUSTER_ENV_MAP` to map cluster names to environments, e.g.
`WL_CLUSTER_ENV_MAP="prod-cluster=prod,staging-cluster=staging"`. A cluster
missing from the map falls back to the `dev` environment.

## SSO (optional)

Human login via the org Keycloak is off unless both `WL_OIDC_ISSUER` and
`WL_OIDC_CLIENT_ID` are set; unset behaves as before (tokens minted only by an
admin or the bootstrap token). When enabled:

| Var | Meaning |
|---|---|
| `WL_OIDC_ISSUER` | e.g. `https://auth.sunstoneinstitute.ai/realms/sunstone` |
| `WL_OIDC_CLIENT_ID` | e.g. `work-tracker` |
| `WL_PUBLIC_URL` | external base URL, for the web login callback |
| `WL_SESSION_SECRET` | HMAC key for web session cookies (required when OIDC is enabled) |

Users then run `wl login` to obtain a 30-day `wl_` token from their SSO
identity. Agent/service tokens are unchanged.

The web session cookie is `Secure`, so web login requires the server to be
reached over HTTPS (or `localhost`); the `wl login` CLI flow is unaffected.

## Cluster watcher

`wl watch` runs a pod informer against one cluster and reports crash loops
and OOM kills to the server:

```bash
wl watch --kubeconfig ~/.kube/config --cluster dev \
  --server http://localhost:8080 --token $WL_TOKEN
```

`--server`/`--token` default to the `WL_SERVER`/`WL_TOKEN` environment
variables, same as the CLI client. Omit `--kubeconfig` when running in-cluster
(it falls back to the in-cluster config).

## Backups

The `backup` Compose profile runs [litestream](https://litestream.io) to
continuously replicate `data/wl.db` to S3-compatible object storage (Hetzner
Object Storage, or any other S3-compatible provider — see the comments in
`litestream.yml`):

```bash
export LITESTREAM_BUCKET=sunstone-wl-backups
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

Migrations live under `deploy/base/migrations/` and use
[golang-migrate](https://github.com/golang-migrate/migrate). They are no
longer embedded in the binary or applied automatically — `wl serve` expects
the schema to already exist. Apply them explicitly with
`wl migrate --db <path> --migrations-path deploy/base/migrations` (the
`docker-compose.yml` `migrate` service does this before `tracker` starts;
in Kubernetes an initContainer does the same from a ConfigMap).
Never edit a migration that has already shipped — add a new pair instead:

```
NNNN_name.up.sql
NNNN_name.down.sql
```

where `NNNN` is the next sequence number.
