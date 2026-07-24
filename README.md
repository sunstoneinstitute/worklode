# worklode

## What it is

worklode is Sunstone Institute's org-wide work tracker: one authoritative
view of planned and in-progress work across all repos, replacing a
hand-maintained `TASKS.md` + GitHub issue sync. It ships as a single Go
binary, `lode`, backed by a PostgreSQL database, with an append-only event log
giving full provenance for every state change. Work arrives from three
sources — a GitHub App (issues, PRs, reviews, CI, releases), a Flux
notification-controller webhook (deployments), and a Kubernetes pod watcher
(crash loops, OOM kills) — and is read back through the `lode` CLI or a
read-only web UI. See `docs/spec.md` for the full design.

## Quickstart

Start the stack with Docker Compose — it brings up Postgres, runs the
migrations, then starts the server. The server reads its database connection
from `LODE_DSN` (a `postgres://...` DSN; the `--dsn` flag overrides it), which
the compose file already sets. `LODE_BOOTSTRAP_TOKEN` creates the initial
admin actor the first time the database is empty. It must match
`^wl_[0-9a-f]{40}$` — the exact form `wl_$(openssl rand -hex 20)` mints.
A bare `openssl rand -hex 20` (no `wl_` prefix) fails validation at startup:

```bash
export LODE_BOOTSTRAP_TOKEN=wl_$(openssl rand -hex 20)
docker compose up -d
```

Install the `lode` CLI:

```bash
go install ./cmd/lode    # or: go build -o ~/bin/lode ./cmd/lode
```

Point the CLI at it, either via `~/.config/worklode/config.toml`:

```toml
server = "http://localhost:8080"
token = "wl_..."   # the LODE_BOOTSTRAP_TOKEN value
```

or via environment variables:

```bash
export LODE_SERVER=http://localhost:8080
export LODE_TOKEN=$LODE_BOOTSTRAP_TOKEN
```

Then create a project, map a repo to it, add a task, and claim it:

```bash
lode project add sunstone-web --name "Sunstone Web"
lode project add-repo sunstone-web sunstoneinstitute/sunstone-web
lode task add --project sunstone-web --title "Fix the footer link"
lode task claim <task-id>
```

Managing projects, actors, and tokens requires an admin actor; the
bootstrap actor is admin, and `lode actor add --admin` creates more.

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
- **Webhook secret**: a random string, set as `LODE_GITHUB_WEBHOOK_SECRET` on
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
  name: worklode
  namespace: flux-system
spec:
  type: generic-hmac
  address: https://<host>/hooks/flux
  secretRef:
    name: worklode-hmac
---
apiVersion: v1
kind: Secret
metadata:
  name: worklode-hmac
  namespace: flux-system
stringData:
  hmac-key: <same value as LODE_FLUX_WEBHOOK_SECRET>
---
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Alert
metadata:
  name: worklode
  namespace: flux-system
spec:
  providerRef:
    name: worklode
  eventSeverity: info
  eventSources:
    - kind: Kustomization
      name: "*"
    - kind: HelmRelease
      name: "*"
```

Set `LODE_FLUX_WEBHOOK_SECRET` on the server to the same HMAC key, and
`LODE_CLUSTER_ENV_MAP` to map cluster names to environments, e.g.
`LODE_CLUSTER_ENV_MAP="prod-cluster=prod,staging-cluster=staging"`. A cluster
missing from the map falls back to the `dev` environment.

## SSO (optional)

Human login via the org Keycloak is off unless both `LODE_OIDC_ISSUER` and
`LODE_OIDC_CLIENT_ID` are set; unset behaves as before (tokens minted only by an
admin or the bootstrap token). When enabled:

| Var | Meaning |
|---|---|
| `LODE_OIDC_ISSUER` | e.g. `https://auth.sunstoneinstitute.ai/realms/sunstone` |
| `LODE_OIDC_CLIENT_ID` | e.g. `worklode` |
| `LODE_PUBLIC_URL` | external base URL, for the web login callback |
| `LODE_SESSION_SECRET` | HMAC key for web session cookies (required when OIDC is enabled) |

Users then run `lode login` to obtain a 30-day `wl_` token from their SSO
identity. Agent/service tokens are unchanged.

The web session cookie is `Secure`, so web login requires the server to be
reached over HTTPS (or `localhost`); the `lode login` CLI flow is unaffected.

## Cluster watcher

`lode watch` runs a pod informer against one cluster and reports crash loops
and OOM kills to the server:

```bash
lode watch --kubeconfig ~/.kube/config --cluster dev \
  --server http://localhost:8080 --token $LODE_TOKEN
```

`--server`/`--token` default to the `LODE_SERVER`/`LODE_TOKEN` environment
variables, same as the CLI client. Omit `--kubeconfig` when running in-cluster
(it falls back to the in-cluster config).

## Backups

Backups are owned by CNPG (CloudNativePG) in-cluster; the compose stack has
no backup mechanism of its own.

## Development

Requires Go 1.25. Run the test suite with:

```bash
go test ./...
```

Store tests need a reachable Postgres (default
`postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`,
override with `TEST_POSTGRES_DSN`); each test creates and drops its own
ephemeral database. Tests skip when Postgres is unreachable, unless `CI` is
set.

Migrations live under `deploy/base/migrations/` and use
[golang-migrate](https://github.com/golang-migrate/migrate). They are no
longer embedded in the binary or applied automatically — `lode serve` expects
the schema to already exist. Apply them explicitly with
`lode migrate --dsn <postgres-dsn> --migrations-path deploy/base/migrations`
(the `docker-compose.yml` `migrate` service does this before `worklode`
starts; in Kubernetes an initContainer does the same from a ConfigMap).
Never edit a migration that has already shipped — add a new pair instead:

```
NNNN_name.up.sql
NNNN_name.down.sql
```

where `NNNN` is the next sequence number.
