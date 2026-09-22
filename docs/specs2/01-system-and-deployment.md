# System and deployment

Worklode is Sunstone's org-wide work tracker and coordination layer for multi-agent, multi-repo work. It ships as six Go executables from one module, backed by Postgres with an append-only event log. This document opens the set. It holds the shared glossary, the executables and their import boundaries, how the prod and dev instances are deployed, how worker sandboxes are provisioned, the Prometheus metrics conventions, how agent token usage reaches the server through OpenTelemetry, and how project overhead cost is stored and reported. Everything else is in the nine sibling documents listed in section 1.

## 1. How to read this set

The ten files under `docs/specs2/` describe Worklode as it stands today. Read them in order the first time. Each file owns its subject. A sibling refers to that subject with one sentence and a filename.

| File | Covers |
|---|---|
| `01-system-and-deployment.md` | Glossary, executables, deployment, sandboxes, metrics, usage accounting, overhead cost (this file) |
| `02-identity-actors-and-secrets.md` | Actors, tokens, permissions, OIDC and GitHub App identity, task secrets |
| `03-tasks-and-execution.md` | Tasks, leases, claims, ranking, edges, participants, milestones, deliverables |
| `04-done-verification-workflows.md` | What "done" means, verification, and the workflows that check it |
| `05-documents.md` | Specs and plans as rows, sections and anchors, lifecycle, amend and supersede |
| `06-design-queries-intents-decks.md` | Design queries, intents, standing queries and decks |
| `07-knowledge-graph-and-search.md` | The knowledge graph projection, corpus index and hybrid search |
| `08-agent-harness-and-sessions.md` | Lifecycle hooks, `lode install`, agent sessions, worktrees, the plugin |
| `09-cli-and-skills.md` | CLI naming rules, command surface, the org skill registry |
| `10-cockpit.md` | The web cockpit: pages, session gate, Progress page, live stream |

## 2. Glossary

Short definitions of the terms the set uses. Where a sibling explains a term in depth, the sibling is the authority. Cross-reference shorthand: `WL-SPEC-25#sec-9` is project key, document kind and number, then a section anchor. `WL-123` is a task id, project key then per-project number. `{#sec-6.1}` is the anchor a section carries for the life of its document.

| Area | Term | Meaning |
|---|---|---|
| Backbone | Backbone | The Postgres database holding the facts about work. Source of truth for agents and people. |
| Backbone | Task | One unit of work that can be claimed, worked and finished. Kinds: `feature`, `bug`, `chore`, `design`, `decision`, `review`, `spike`, `rally`. Priorities: `critical`, `high`, `medium`, `low`. |
| Backbone | Worktree | An isolated checkout of a repository on its own branch. One worktree per task in progress. |
| Backbone | Lease | The claim tying one task to one worktree while work is in progress. Only the lease holder may update the task. |
| Backbone | Claim | Taking an unclaimed ready task and leasing it to a worktree, in one atomic transaction. |
| Backbone | Event log | Append-only record of everything that happened to a task or document. Never edited, so it is the audit trail. |
| Backbone | Edge | A typed link between two tasks or two documents: `blocks`, `child_of`, `follow_up_to`, `duplicate_of`. A task with children is a container by having them. |
| Backbone | Ranking and pickup | The rule that picks which ready task an agent claims next. |
| Backbone | Focus | Narrows pickup to an area (a project, a component). `--strict-focus` refuses to fall back outside it. |
| Backbone | Concern | Narrows pickup further to a category of problem, for example completeness. |
| Backbone | needs-decomposition | Flag on a task too large to work directly; it should be split into children first. |
| Documents | DesignDoc | A spec or plan tracked as a first-class row. |
| Documents | Spec | A document stating an intent meant to stay true after it is built. Drift is checked against it. |
| Documents | Plan | An executable document: task definitions plus instructions. Accepting a plan mints one task per definition. A plan is spent when its tasks are done. |
| Documents | Section | An addressable part of a spec with a stable anchor such as `#sec-4.2`. |
| Documents | Status | `draft`, `accepted`, `stale`, `superseded`, or `withdrawn`. |
| Documents | Covers | A plan's promise to build a spec section, fully, partially, or explicitly none of it. |
| Documents | Defers | A plan's handoff of a section to another named document. |
| Documents | Amend / supersede | Ways a later document changes an earlier one. Amending adjusts a section, superseding replaces it. Original text and numbering stay in place. |
| Graph | Knowledge graph | A queryable graph built by projecting facts out of the backbone, git hosting, CI and deploys. |
| Graph | Component | A software part, usually a directory or package, the smallest unit for ownership and coverage. |
| Graph | Deliverable | A declared definition of done, checked against something concrete that exists. |
| Graph | Effect / AcceptedDeviation | A tolerated deviation from a spec, and the record of which document approved tolerating it. |
| Graph | Drift | A place where reality does not match what a spec says. |
| Graph | Issue / Pull Request | A tracker ticket and a proposed change, projected from the hosting provider and mirrored to a task. |
| Graph | Skill | One org-wide reusable instruction set an agent loads before a task, identified by name and source repository. |
| Graph | Artifact, Build, Deployment, Environment, Commit, Runtime event | A built versioned unit; the CI run that produced it; one rollout to a target; a deployment stage; one change on a default branch; an observed event on a live deployment such as a crash loop or OOM kill. |
| People | Project | The umbrella for work across a set of repositories. |
| People | Crew | The people currently working a project. Leaving the Crew sends open work through a handoff review. |
| People | Actor | Whoever performed an action, person or agent. Automatic actions on a person's behalf are labelled as such. |
| People | Approval | A recorded decision that a document or deliverable is accepted, with who and when. |
| People | Milestone | A cross-plan grouping of deliverables meant to ship together. See `03-tasks-and-execution.md`. |

Groupings are queries. A plan's tasks, a milestone's deliverables and a project's work are computed from the facts, never stored as a second copy that can drift.

## 3. Executables and import boundaries

### 3.1 The six executables

All six come from one Go module and one release version, and expose the same stamped version from one shared package.

| Executable | Contract | Installed for |
|---|---|---|
| `lode` | Human-facing CLI and HTTP client | end users |
| `lode-hook` | Short-lived lifecycle and Git hook runner | end users |
| `lode-statusline` | Short-lived status-line renderer | end users |
| `lode-server` | HTTP servers and server-owned background loops | server image |
| `lode-watch` | Kubernetes pod watcher | watcher image |
| `lode-migrate` | Postgres schema migration runner | server image, init container |

`lode-hook` takes an event and its hook arguments, or `--harness`, `--next`, `--list`. `lode-statusline` takes the harness stdin payload and emits the status line. The server, watcher and migrator take their flags directly with no subcommand word.

### 3.2 Dependency boundaries

Each `cmd/<name>` entry point imports only its application package and shared leaf packages. `internal/cmd` is the `lode` CLI surface and imports none of `internal/api`, `internal/store`, `internal/watch`, `internal/hookrun`, `internal/statusline`.

`lode-hook` and `lode-statusline` parse arguments with the standard library. Their transitive imports must not contain Cobra, the CLI command tree, Goldmark, server or store packages, Prometheus, or Kubernetes packages. `internal/disttest/deps_test.go` fails the build when one creeps in.

Server-owned loops stay in `lode-server`: lease sweeping, document lifecycle, skill sync, graph projection. They share server configuration, storage, metrics and shutdown. `lode task reconcile` and `lode blob gc` are operator commands in `lode` that call authenticated server APIs. Interactive streaming commands such as `lode event tail --follow` stay in the CLI.

### 3.3 Managed bindings

`lode install` writes lifecycle bindings as `lode-hook ...` and the status-line binding as `lode-statusline`. Upgrade and uninstall touch only those bindings and leave unrelated user configuration alone.

### 3.4 Distribution

| Channel | Contents |
|---|---|
| Homebrew | `lode`, `lode-hook`, `lode-statusline`; tests their version output |
| Windows release archive and Scoop manifest (`sunstoneinstitute/scoop-bucket`) | `lode.exe`, `lode-hook.exe`, `lode-statusline.exe` |
| Server container | `lode-server` (entry point), `lode-migrate`, migrations, optional ffmpeg. The Kubernetes init container runs `lode-migrate` directly. Distroless, non-root, no shell. |
| Watcher container | `lode-watch` only, published separately so Kubernetes client code is absent from the user and server distributions |

Each executable has its own build target and one aggregate target builds all six. CI cross-compiles the three user executables for Windows and builds both images.

### 3.5 Failure behaviour

Worklode's own hook action never fails the triggering event. A chained command's exit status is propagated. The status-line renderer never produces a harness-breaking error. Server, watcher and migrator startup failures are fatal and written once to stderr.

Startup time and installed size are review evidence, recorded on one machine. They are not CI gates. Correctness and dependency boundaries are.

## 4. Deployment

### 4.1 Where each instance runs

| Instance | Cluster | Overlay | Host |
|---|---|---|---|
| prod | admin | `deploy/overlays/admin/` | `worklode.sunstoneinstitute.ai` |
| dev | hzdev | `deploy/overlays/hzdev/` | `worklode.dev.sunstoneinstitute.ai` |

Prod runs in the admin cluster because reaching it is worth the org: every task, brief and event across projects, token minting through the bootstrap token in the pod environment, encrypted GitHub user tokens whose key sits in the same pod, approvals, and the Postgres behind all of it. The admin cluster already holds Keycloak and has the narrowest set of people and pipelines that can change it. `hzprod` runs production workloads with a one-application blast radius, and the observer should not share the fate of what it observes.

Running in the admin cluster grants Worklode no admin-cluster privilege. The GitHub App stays installed on selected repositories with the provisioning and admin-cluster repos excluded, and its ceiling stays read-only on contents. Widening that is a change to `02-identity-actors-and-secrets.md`.

### 4.2 Instance environment

`LODE_CLUSTER_ENV_MAP` maps a cluster name to the environment of deployments Worklode observes (`env_deploys`, Flux webhooks). Both overlays carry `admin=prod` in it. It says nothing about where Worklode runs and must not be repurposed to.

A second, unrelated fact says which kind of instance the server is.

| | |
|---|---|
| Env var | `LODE_INSTANCE_ENV` |
| Values | `dev`, `prod`. Any other value fails startup. |
| Default | `prod` |
| Set in | `dev` in the hzdev overlay and `docker-compose.yml`, `prod` in the admin overlay |

The default is `prod` because a prod instance that believes it is dev drops real decision records, and the reverse only asks for an unneeded justification. This section defines the field only. Behaviour keyed on it, such as deletion requiring a justification on prod, belongs to the document introducing that behaviour.

### 4.3 The prod overlay

`deploy/overlays/admin/` differs from hzdev in:

- Ingress host `worklode.sunstoneinstitute.ai` in both TLS hosts and the rule host.
- `LODE_PUBLIC_URL` set to that host. Login and webhook callbacks render from it.
- `secretStoreRef` names the admin cluster's `ClusterSecretStore`. 1Password item and property names match hzdev.
- `LODE_INSTANCE_ENV: prod`.
- OIDC client `worklode-prod` against the same issuer.
- `LODE_WEB_OPEN` is never set, here or on any other reachable instance. The web routes refuse to serve with no provider configured, and that opt-in is the only way around it. Placement never substitutes for the session gate (`10-cockpit.md`).

`LODE_CLUSTER_ENV_MAP` is identical in both overlays. The namespace carries `sunstone.institute/ghcr-pull` in both.

### 4.4 Scrape path

The ClickStack collector discovers targets by Service annotations, so a dedicated single-port `worklode-metrics` Service (`deploy/base/metrics-service.yaml`) carries `prometheus.io/scrape: "true"`. Scraped metrics flow directly to ClickHouse, bypassing the otel-gateway. Pod-template annotations carry nothing.

## 5. Sandboxes and worker environments

A coding agent in a provider-managed cloud sandbox has a fresh checkout, an ephemeral filesystem, no keychain and no operator. It is an ordinary CLI client: the backbone is reachable over the public API ingress, so it claims, heartbeats and closes tasks as a laptop does. Leases and worktrees, commits as heartbeat, the `LODE_SERVER`/`LODE_TOKEN` environment override and lazy per-task skill fetch all apply. What the sandbox needs is provisioning, plus one ordering rule (5.5).

### 5.1 The bootstrap is repo-committed

`scripts/bootstrap.sh` takes a bare checkout to a working environment and is the only description of that environment. CI, the worker image build, a laptop and a sandbox all call it. It reads each tool's version from its one source instead of restating it.

| Tool | Single source | Consumed via |
|---|---|---|
| Go | `go.mod` | `go-version-file: go.mod` in every workflow |
| templ | `tool github.com/a-h/templ/cmd/templ` in `go.mod` | `go tool templ` |
| Tailwind | `scripts/tailwind.sha256` (version plus per-platform SHA256) | `scripts/fetch-tailwind.sh` |

No tool manager (`mise`, `.tool-versions`) is used. The trigger to adopt one is a second runtime needing a pin no existing source carries, which the `plugins/obsidian/` Node toolchain is a candidate for. System packages live in the Dockerfile, which is Linux-only. `bootstrap.sh` fails loudly naming what a laptop lacks. No cross-platform package-name schema exists.

### 5.2 Worker images

An image is a cache of what `bootstrap.sh` would do. An image whose contents disagree with the script is a defect in the image. The root `Dockerfile` is the server image and is hostile to being a worker (no shell, no package manager), so workers are a separate family.

| Image | Contents | Purpose |
|---|---|---|
| `docker/base/Dockerfile` | shell, `git`, CA certificates, `lode` | The minimum that can claim a task and run a hook |
| `docker/generic/Dockerfile` | base plus the toolchains `bootstrap.sh` installs | Default when a project declares nothing |
| `<repo>/.worklode/Dockerfile` | `FROM` one of the above | A project's own additions, versioned with the project |

Worker images run `lode install --agent all --no-statusline` at build time. The status line has no reader in a sandbox.

### 5.3 Content-addressed tags

A worker image tag is a digest over its inputs: the base image digest, `.worklode/Dockerfile`, and the pin files the bootstrap reads (`go.mod`, `scripts/tailwind.sha256`). A starting worker requests the tag its checkout implies. Finding it is the fast path. Not finding it is the build trigger. No change detector exists because a tag that exists is by construction built from those inputs.

### 5.4 Egress

A worker's outbound network is deny by default. The baseline allowlist:

- the Worklode API ingress (`LODE_SERVER`)
- the model provider endpoint the harness talks to
- the git hosts of the project's mapped repos
- language-ecosystem registries (`proxy.golang.org`, `sum.golang.org`, `registry.npmjs.org`, `pypi.org`, `files.pythonhosted.org`), because a checkout's dependencies move faster than its image
- the worker-image registry

A project needing more lists hosts one per line in `.worklode/egress.txt`, reviewed with the code that needs it. The mechanism (NetworkPolicy, egress proxy, provider allowlist) belongs to the deployment holding the cloud credentials. Exfiltration through an authorised push to the git host remains possible. Credential scoping in `02-identity-actors-and-secrets.md` is the primary control.

### 5.5 The sandbox session

| Concern | Rule |
|---|---|
| Entry point | `lode work next --json`, then work the worktree it returns. The `/lode:*` plugin commands are unavailable: a plugin install needs a session restart, which a sandbox cannot do. |
| Hooks | Installed at image build time. The agent is already running when the checkout appears, so hooks written then miss that session's `SessionStart`. |
| Identity | `LODE_SERVER` and `LODE_TOKEN` in the environment. The token is a task-scoped token minted through `POST /api/v1/tasks/{id}/tokens`, bound to the task, attributed to an agent actor, expiring and revoked with the lease. Its model is in `02-identity-actors-and-secrets.md`. |
| Secrets | Task secrets are in `02-identity-actors-and-secrets.md`. A task needing a secret the sandbox cannot materialise is not a sandbox task. |
| Human questions | `lode channel serve` is a local stdio MCP relay that polls the backbone and delivers `lode task instruct` rows into the live session. |

The MCP relay is registered by an `.mcp.json` entry in the checkout plus two launch flags. The build-time rule covers hooks only.

### 5.6 Seams for backbone-initiated dispatch

The backbone does not launch sandboxes. When agent pools do (`10-cockpit.md` for the confirmation surface), dispatch is the human path with parameters substituted.

| Seam | Human path | Dispatch substitutes |
|---|---|---|
| Image selection | The tag the checkout implies | The same tag, resolved server-side |
| Provisioning | `bootstrap.sh` | The same script |
| Token acquisition | `LODE_TOKEN` in the environment | A minted task-scoped token in the same variable |
| Entry point | `lode work next --json` | The same command with the task id supplied |

The executor behind the seam is a Kubernetes `Job`. `kagent` is declined because it owns no task-scoped checkout or lease lifecycle and keeps its own execution history. `agent-sandbox` is declined because its warm-pool advantage needs a claim-then-push shape that changes two seams. Re-evaluate it, against a plain `Job`, when dispatch is designed to drive a running pod.

## 6. Metrics conventions

The server carries a private Prometheus registry with the Go and process collectors, `http_requests_total` and `http_request_duration_seconds` middleware with mux-pattern route labels, and `/metrics` on the admin listener (port 9090). Nothing lands on the public mux.

Rules for every metric:

- `lode-server`'s main package is the composition root. It creates the registry and passes a `prometheus.Registerer` into each subsystem.
- `api.NewServer` takes the registry, registers the HTTP middleware metrics, and serves `promhttp.HandlerFor` with `ContinueOnError` and an `ErrorLog`. A failing collector drops its family and surfaces as `promhttp_metric_handler_errors_total{cause="gathering"}`. With no registry passed it falls back to a private one.
- Every other subsystem owns its instruments in a package-private `metrics.go` next to the code it measures. No central metrics package.
- Every metrics struct is nil-safe. A nil receiver no-ops, so the CLI and tests pay nothing.
- Every metric is prefixed `worklode_`, except the `http_requests_total` and `http_request_duration_seconds` pair.
- Label values are bounded. `outcome` names a domain result from the store's sentinel errors. `result` names plain success or failure. Do not mix them.
- A cancelled context records nothing. A deadline exceeded counts as `error`.
- Server-side changes adding an endpoint, background loop, outbound call or store operation with meaningful outcomes add or extend `worklode_*` metrics in the owning package, with `prometheus/testutil` assertions and the `TestMetricsEndpointDomainFamilies` family check.

`store.Open` takes `store.WithMetrics(reg)`, which registers `collectors.NewDBStatsCollector(db, "worklode")` (the pool is capped at 16, waits are the saturation signal) and the store's domain metrics.

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
| `worklode_webhook_events_total` | counter | `source` = `github`, `flux`, `catalog`; `event`; `result` = `ok`, `rejected`, `ignored`, `unrouted`, `error` | `internal/hooks` |
| `worklode_catalog_evidence_total` | counter | `state`; `entity_kind` = `deliverable`, `task`, `doc`; counted after commit | `internal/hooks` |
| `worklode_embed_requests_total` | counter | `result` = `ok`, `error`; one per `Embed` call | `internal/embed` |
| `worklode_embed_request_duration_seconds` | histogram | 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10 | `internal/embed` |

Webhook `event` values: for GitHub, the `X-GitHub-Event` value when the handler switches on it (`issues`, `push`, `pull_request`, `pull_request_review`, `deployment_status`, `workflow_run`, `release`), else `other`. For Flux, `flux`. For the data catalog, the artifact state `published`, `updated`, `deprecated`, `removed`, `failed`, or `invalid` when rejected before a state could be read. `unrouted` is catalog-only: the delivery was authentic and stored but no open entity had declared the artifact. A partial skill sync records its items and an `error` run.

## 7. Agent usage accounting

Token usage reaches Worklode from the running agent's own OpenTelemetry export, received by the local Edge Agent. Session rows, the task and project-overhead buckets (section 8), effective-dated `model_prices` pricing, and replace-not-accumulate reporting apply regardless of transport.

### 7.1 Collection and normalisation

| Harness | Export | Edge Agent reads |
|---|---|---|
| Claude Code | `claude_code.token.usage` metrics | token type, model, session id, timestamp, agent or subagent identity, resource attributes. `claude_code.cost.usage` is ignored; Worklode prices from `model_prices`. |
| Codex | OTel log records (the log exporter is required; its metrics carry no token counter) | token counts from completed-response events, normalised to the same usage classes |

Edge Agent keeps a durable local store keyed so a retried OTLP delivery is idempotent, and posts complete replacement totals to `POST /api/v1/projects/{id}/session-usage`. It never posts deltas. Export failure never blocks the coding agent. Edge Agent retries from its store. A stopped or unreachable Edge Agent may leave a gap, which `lode doctor` reports.

### 7.2 Attribution

Claude Code is launched with resource attributes `worklode.project.id` and `worklode.task.id` when Worklode knows them. A task id is set only for a process launched for that task. Attributes inherited from a parent session are not proof a subagent worked the same task. When the harness exposes a child session id, Worklode may bind it to the claimed task at launch. Otherwise usage is reported against the project with no task id and lands as project overhead. Project attribution is the required floor. Task attribution is best effort and never guessed from token timing. Codex has no custom resource attribute surface, so its usage joins a project through the local launch context when available and is otherwise overhead.

### 7.3 Installation and cleanup

`lode install` configures each harness to export to the local Edge Agent: Claude Code through `CLAUDE_CODE_ENABLE_TELEMETRY`, the OTLP exporter and endpoint variables, and `OTEL_RESOURCE_ATTRIBUTES`; Codex through `[otel]` in `$CODEX_HOME/config.toml`, user-level and not per worktree. Lifecycle hooks open, heartbeat, close or release a session or lease and carry no token accounting. `lode install` is idempotent and never touches foreign hooks. No content telemetry is enabled: no prompts, responses, raw API bodies, tool content or tool arguments. Hook details are in `08-agent-harness-and-sessions.md`.

### 7.4 Historical import

`internal/transcript` parses Claude Code transcripts for historical import and has no live caller. An import posts to the same replacement endpoint, so a session already seen through OTel is replaced, never doubled. It classifies a transcript's turns by the working directory each was recorded in, resolved to a task worktree of this repository or to overhead, so the split in section 8 holds for imported data too.

## 8. Project overhead cost

Tokens that belong to no task still cost money. An orchestrator session running from a repository's main checkout, a worktree whose lease this actor no longer holds, or a directory outside the configured worktree layout all bill to a project-level overhead bucket. Overhead is never folded into a task and never dropped.

### 8.1 Schema

Overhead has no lease and no task, so it cannot live in `agent_sessions` or `agent_session_usage`. Two tables hold it.

`project_overhead_usage`, one row per (project, agent, external session id, day, model, speed), replaced wholesale per (project, agent, external session id):

| Column | Type | Constraint |
|---|---|---|
| `project_id` | text | FK `projects(id)` ON DELETE CASCADE |
| `agent`, `external_session_id` | text | NOT NULL |
| `usage_day` | date | NOT NULL |
| `model` | text | NOT NULL |
| `speed` | text | `standard` (default) or `fast` |
| `input_tokens`, `cache_write_5m_tokens`, `cache_write_1h_tokens`, `cache_read_tokens`, `output_tokens` | bigint | NOT NULL, >= 0, default 0 |
| `cost_amount` | numeric(14,6) | NULL means no price on file for that model and day |
| `cost_currency` | text | `^[A-Z]{3}$`, default `USD` |

Primary key is the full six-column key. Index `project_overhead_usage_day` on `usage_day` supports the rollup recompute.

`project_daily_overhead_cost` is the derived rollup, keyed by (`project_id`, `usage_day`, `cost_currency`), with the five token columns, `cost_amount numeric(14,6) NOT NULL DEFAULT 0` and `unpriced_tokens bigint`. It is recomputed from scratch for every affected (project, day) when a report is replaced. It is its own table so neither rollup can absorb the other's rows and a reader can tell task spend from overhead at the storage layer. `model_prices` is shared. Overhead is priced by `modelPriceFor` exactly as task usage is.

### 8.2 Store and API

`(*Store) ReportProjectSessionUsage(ctx, projectID, agent, externalSessionID string, byTask map[string][]SessionUsageBucket) error` replaces one session's complete usage across a project in one transaction. Task keys land on their `agent_sessions` row through `applySessionUsageTx`. The `""` key lands on `project_overhead_usage`. Both rollups are rebuilt for every affected day. Rules:

- `agent` is validated against the same vocabulary `TouchAgentSession` uses, and an empty `externalSessionID` is rejected. Both are `ErrInvalidInput`. An unknown project is `ErrNotFound`.
- No lease-holder check. The write reaches rows whose lease is gone, and any authenticated actor may report for any project it can name.
- A task named in `byTask` with no `agent_sessions` row bills to overhead.
- A task that had a row and is absent from `byTask` has its usage cleared.

Whole-session scope is what makes a double count impossible to represent. A turn's destination can change between two reports (a directory resolves to a task while the lease is held and to overhead after). Replacing per destination would leave the vacated side holding its copy. There is no overhead-only endpoint for the same reason.

| Route | Body | Response | Permission |
|---|---|---|---|
| `POST /api/v1/projects/{id}/session-usage` | `{agent, external_session_id, by_task}`, all required; `by_task` maps a task id or `""` to `SessionUsageBucket` arrays | 204; 400 malformed; 404 unknown project | `project.report`, granted to every authenticated role |

`project.report` is separate from `task.claim` because reporting spend is not a claim on a task. Permissions are defined in `02-identity-actors-and-secrets.md`.

### 8.3 Cost reports

`(*Store) ProjectCost` reads `project_daily_cost` and `project_daily_overhead_cost` for the window and full-outer-joins them per (`usage_day`, `cost_currency`), so a day with overhead only still produces a row. Each `CostDay` and `CostTotal` carries the combined total in its top-level `Tokens`, `Cost` and `UnpricedTokens`, plus overhead's own share nested under `overhead`:

```go
type CostOverhead struct {
	TokenCounts
	CostAmount     string `json:"cost_amount"`
	UnpricedTokens int64  `json:"unpriced_tokens"`
}
```

It is nested so its keys cannot collide with the combined totals. `TaskCost` carries no overhead share. Currency conversion between buckets does not exist. Both are summed within one currency.

### 8.4 Cockpit

The project cockpit's automation-boundary card shows the combined "Agent spend, 30 days" figure and one added line with the overhead share (`ui.CockpitCostTotal.OverheadCostAmount`). No surface lists overhead by session. The cockpit is in `10-cockpit.md`.

## 9. Verification

| Area | Check |
|---|---|
| Executables | Each entry point's argument contract; dependency test over `lode-hook` and `lode-statusline` transitive imports; managed-binding upgrade and uninstall; Windows archive and Scoop manifest expose three user executables; Homebrew tests the same three; container checks for server entry point, migrate init command and watcher image |
| Sandbox | A session holding only `LODE_SERVER` and `LODE_TOKEN`, no config file, no keychain, claims and closes a task through `e2e/`; CI builds `docker/base` and `docker/generic` and runs `bootstrap.sh` inside each; changing a tagged input changes the tag and changing nothing keeps it |
| Metrics | Per-package tests with a fresh registry and `testutil`; `TestMetricsEndpointDomainFamilies` wires one registry through store and server and asserts the families on admin `/metrics`; `TestHealthzAndMetricsNotOnPublicHandler` |
| Usage accounting | Repeated OTLP delivery yields the same total; a Codex completed-response log yields usage without a rollout file; a bound task session is charged to its task and an unbound parent session once to overhead; Edge Agent restart preserves unreported usage; `lode install` converges on a second run; no installer path enables content telemetry |
| Overhead | A repeat report replaces; a shrinking report drops a day's rollup row; a session's tokens count once whichever bucket they land in, in both reclassification directions; `ProjectCost` combines and `TaskCost` is unaffected; route-guard boot check covers the route |

## Sources

WL-SPEC-2 (glossary), WL-SPEC-53 (executable and distribution boundaries), WL-SPEC-39 (prod in the admin cluster), WL-SPEC-38 (cloud sandbox), WL-SPEC-22 (Prometheus metrics), WL-SPEC-63 (OpenTelemetry usage accounting), WL-SPEC-52 (project overhead cost).

## Open questions

- The question direction for the MCP relay: a pending-question row, an answer surface, a worker-side wait, and a `PreToolUse` hook redirecting `AskUserQuestion` to it.
- Where worker images live and who may pull them. The registry's reachability rides the egress baseline.
- Whether the sandbox provider guarantees a setup step before the agent session starts. If not, the image is the only supported sandbox path.
- What builds a project's `.worklode/Dockerfile` on first use, and who pays for the wait.
- Whether Codex will expose a stable attribution field equivalent to `worklode.task.id`.
- A session working in another repository's main checkout still bills to this project's overhead in the import path, because nothing in the path says otherwise (WL-329).
