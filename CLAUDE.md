# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Worklode is Sunstone's org-wide work tracker and coordination layer for
multi-agent, multi-repo work. It ships as a single Go binary, `lode`, that is
server (`lode serve`), CLI client, Kubernetes pod watcher (`lode watch`), and
migrator (`lode migrate`) in one, backed by Postgres with an append-only event
log for provenance. Design lives in `docs/specs/` (numbered, flat); start with
`000-umbrella-architecture.md`, which maps all sub-specs. Implementation plans
live in `docs/plans/`; `docs/follow-ups.md` holds known non-blocking gaps —
check it before filing something as new.

## Commands

```bash
go build ./...                      # build everything
go install ./cmd/lode               # install the CLI
go test ./...                       # unit + store tests
go test ./internal/store -run TestClaim   # single test
go test -race -count=1 -tags e2e ./e2e/   # e2e suite (build tag required)
gofmt -l . && go vet ./...          # lint (what CI runs)
./scripts/check-migrations.sh --no-fix    # migration-number collision check
```

Store tests need a reachable Postgres with **pgvector**
(CI uses `pgvector/pgvector:pg17`); default DSN
`postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`,
override with `TEST_POSTGRES_DSN`. Each test creates and drops its own
database. Tests skip silently when Postgres is unreachable unless `CI` is set
— a green local run without Postgres proves less than it looks like.

Local stack: `export LODE_BOOTSTRAP_TOKEN=wl_$(openssl rand -hex 20)` then
`docker compose up -d` (Postgres + migrations + server; web UI on
`localhost:8080`).

## Migrations

`deploy/base/migrations/`, golang-migrate, `NNNN_name.up.sql`/`.down.sql`
pairs. They are **not** embedded in the binary or auto-applied — `lode serve`
expects the schema to exist (compose `migrate` service / K8s initContainer
apply them). Never edit a shipped migration; add a new pair with the next
number. The pre-commit collision check renumbers your migration automatically
when two branches claimed the same number; new migrations must also be listed
in `deploy/base/kustomization.yaml`.

## Architecture

Request flow: `internal/cmd` (cobra commands, both server and client sides) →
`internal/cli` (HTTP client, project scoping, rendering) → `internal/api`
(HTTP server: `/api/v1` bearer-token API, unauthenticated read-only web UI +
`/metrics`, OIDC login) → `internal/store` (all Postgres access via pgx;
domain logic like task state machine, ranking, atomic `claim` live here).

Ingest paths write through the same store layer: `internal/hooks` (GitHub App
webhooks, Flux notification-controller webhooks — both HMAC-signed),
`internal/watch` (pod informer for crash loops/OOM kills), and `lode inbox
import` (backfill through the webhook store path, so re-running is safe).

Cross-cutting pieces: worktree-bound leases (a claim binds a task to a
`wt/<task-id>-<slug>` worktree; `internal/worktree`, `internal/hookrun` for
the Claude Code hook bindings), agent-session tracking priced from the
agent's own transcript (`internal/transcript`, `store/pricing` — rates are
effective-dated rows in `model_prices`, never hardcoded), and the org skill
registry with pgvector embeddings (`internal/skillsync`, `skillstore`).

The backbone (this repo, Postgres) owns execution facts only; design/
architecture facts belong to the data-platform knowledge graph (spec 003/006).
No fact has two owners — keep new state on the right side of that split.

`e2e/` drives the stack through public surfaces only (HTTP API, signed
webhooks, web pages) — never direct store writes. Keep it that way; it exists
to prove the real user path works.

`www/` is the static marketing site: own deploy workflow, shares no code with
the Go build. Docs-only PRs (only `*.md`, `docs/`, `www/`) skip CI checks;
the `can-be-tested` label forces a run.

## Conventions

- Tokens are `wl_` + 40 hex chars everywhere (bootstrap, minted, e2e
  fixtures); anything else is treated as a token hash, not a plaintext.
- Task branches are `<prefix><task-id>-<slug>` (default prefix `lode/`;
  legacy `wl/` still recognized for correlation). The server is the authority
  on branch names.
- `MODEL_SELECTION.md` defines which Claude model tier each agent role uses
  when working this repo with subagents.

## Metrics

Server-side changes that add an HTTP endpoint, background loop, outbound
call, or store operation with meaningful outcomes must add or extend
`worklode_*` Prometheus metrics in the owning package, with tests. Follow the
conventions in `docs/specs/022-prometheus-metrics.md`: nil-safe metrics
struct in the owning package's `metrics.go`, `prometheus.Registerer`
threaded from `serve.go`, bounded label values, `worklode_` prefix.
