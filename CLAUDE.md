# CLAUDE.md

## What this is

Worklode is Sunstone's org-wide work tracker and coordination layer for
multi-agent, multi-repo work. It ships as a single Go binary, `lode`, that is
server (`lode serve`), CLI client, Kubernetes pod watcher (`lode watch`), and
migrator (`lode migrate`) in one, backed by Postgres with an append-only event
log for provenance. Design lives in `docs/specs/` (numbered, flat); start with
`004-execution-backbone.md`; `docs/specs/index.yaml` is the generated map of
every document's sections.

**To read what a spec says, open `docs/specs/inlined/` — not `docs/specs/`.**
Each file there is one spec with every in-force amendment and supersession
already folded into the text, attributed to the section it came from. Reading
004 in `docs/specs/` tells you what 004 said when it was written; four other
specs have amended it since, and resolving that by hand costs four extra file
reads and gets it wrong when a claim comes from a document that is not accepted
yet. `docs/specs/` stays the corpus of record — **edit there, never in
`inlined/`** — and a pre-commit hook regenerates the views, so they are never
behind. Implementation plans
live in `docs/plans/`; `docs/follow-ups.md` holds known non-blocking gaps —
check it before filing something as new.

## Commands

```bash
go test ./internal/store -run TestClaim   # single test
go test -race -count=1 -tags e2e ./e2e/   # e2e suite (build tag required)
./scripts/check-migrations.sh --no-fix    # migration-number collision check
./scripts/secfmt.py -l              # spec section numbering + anchor check
./scripts/inlinespec.py             # regenerate docs/specs/inlined/
```

Cockpit dev loop (`internal/api` and `internal/ui`'s templ + Tailwind build,
driven by the single `//go:generate` directive in `internal/ui/ui.go`):

```bash
go tool templ generate --watch        # regenerate *_templ.go on change
./bin/tailwindcss -i internal/ui/styles/app.tailwind.css \
  -o internal/ui/assets/app.css --watch
./scripts/fetch-tailwind.sh           # one-time: install the pinned CLI into bin/
go generate ./...                     # regenerate both committed artifacts
```

The Obsidian plugin (`obsidian/`) is the repo's only Node package and uses
**pnpm**, pinned by `package.json`'s `packageManager` and supplied by corepack —
`npm` there will fight the lockfile:

```bash
corepack enable pnpm                            # once per machine
pnpm -C obsidian install --frozen-lockfile
pnpm -C obsidian test                           # also: typecheck, build
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
(HTTP server: `/api/v1` bearer-token API, a session-gated, read-mostly project
cockpit; development mode is open only when `LODE_WEB_OPEN` is set and no
provider is configured, +
`/metrics`, OIDC login) → `internal/store` (all Postgres access via pgx;
domain logic like task state machine, ranking, atomic `claim` live here). The
cockpit's web pages render with `templ` components (`internal/ui/*.templ`,
compiled to `*_templ.go` by `go generate`), styled by a standalone Tailwind
CSS v4 build (`internal/ui/styles/app.tailwind.css` →
`internal/ui/assets/app.css`) and a self-hosted, currently dormant HTMX.
`internal/ui` owns the page components and the design-system assets
(stylesheet, fonts, htmx) served at `/assets/` via `internal/api`'s
`assetHandler`; the components take `internal/ui` view types that
`internal/api`'s `render.go` builds. `internal/ui` depends on nothing beyond
stdlib, `internal/model`, and the templ runtime — `internal/api` imports
`internal/ui`, never the reverse.

**One model, not one per package (ADR 036).** Every shape that crosses the
HTTP boundary — entities, response projections, request bodies — is declared
once in `internal/model` (stdlib imports only). `internal/store` scans into
it, `internal/api` serializes it, `internal/cli` decodes it, `internal/ui`
embeds it. Field names are wire names (`Project`, not `ProjectID`). Only three
kinds of type stay package-local: `internal/ui` view types (page-shell state
and pre-formatted strings, composing model types), `internal/store` scan
plumbing, and `internal/api` transport internals (`Subject`, sessions, route
guards). Anything else needs an amendment to 036, not a new struct.

`internal/model/rule_test.go` decides this (036 §8). It fails the build on a
json-tagged struct — named *or* anonymous — declared in `internal/api` or
`internal/cli`, on a map handed to an HTTP body argument (`writeJSON`'s
third argument, the CLI client's `do` body), inline or built up first, and on
a json-tagged `map[...]any` field in `internal/model` itself — a named
envelope around undeclared entries, which is what `TimelineResponse.Timeline`
was until it became `[]model.TimelineEntry`.
`internal/cmd` is held only to the anonymous-shape rule (its json-tagged
types are `--json` stdout contracts, which cross no HTTP boundary, but must
be named); `internal/hooks` only to the body rule (its declared shapes are
GitHub's and Flux's inbound payloads, which worklode does not own). The
`allowed` map grows only for a type serialized somewhere other than an HTTP
body — keyed by package, with the reason — and a stale entry is reported.
What the guard still cannot see is a body assembled by a helper.
`internal/model/deps_test.go` keeps the package a stdlib-only leaf.

Every route is guarded through one table. `internal/api/router.go`'s
`routeGuards` names the permission each route requires — or `open("why")` when
it deliberately needs no worklode identity — and `NewServer` refuses to boot on
a route the table does not name or a table entry no route uses, so a new
endpoint cannot ship unguarded. `internal/api/authz.go` holds the policy: a
`Subject` derived once per request (bearer token, session cookie, or the
anonymous `authOpen` subject an instance with no login provider serves only
under `LODE_WEB_OPEN`),
a `grants` table of permission → roles, and a default-deny `Decide`. There is
no RBAC model yet — the two roles are the `user`/`admin` Keycloak already
syncs (001 §9.2), and decisions are not project-scoped — so add real roles by
editing that table, never by adding a check inside a handler.

Ingest paths write through the same store layer: `internal/hooks` (GitHub App
webhooks, Flux notification-controller webhooks — both HMAC-signed),
`internal/watch` (pod informer for crash loops/OOM kills), and `lode inbox
import` (backfill through the webhook store path, so re-running is safe).

Cross-cutting pieces: worktree-bound leases (a claim binds a task to a
worktree under `.worktrees/`; `internal/worktree`, `internal/hookrun` for
the Claude Code hook bindings), agent-session tracking priced from the
agent's own transcript (`internal/transcript`, `store/pricing` — rates are
effective-dated rows in `model_prices`, never hardcoded), the org skill
registry with pgvector embeddings (`internal/skillsync`, `skillstore`), and
`internal/eventbus` (offset-tracked subscribers over the events log, spec
025 §15, read via `lode event tail --follow`).

The backbone (this repo, Postgres) owns execution facts and — once spec 025
is implemented — design-document artifacts (specs, ADRs, plans); derived
architecture facts and the queryable view of both belong to the data-platform
knowledge graph (specs 006/025), which receives documents by projection.
No fact has two owners — keep new state on the right side of that split.

## The Obsidian mirror

`obsidian/` is a top-level TypeScript Obsidian plugin, built and shipped
independently of the Go binary, that mirrors a Worklode instance's projects,
docs, and tasks into a machine-owned vault folder. It is a client of the
public `/api/v1` HTTP API only — no store or server access. Read-only but for
one opt-in return path (`writeBack`, default off): a task note's edited body,
pushed with `PATCH /api/v1/tasks/{id}` on a full sync. The backbone wins any
conflict and the local text is preserved as a conflict note. Its wire types
(`obsidian/src/api/types.ts`) are hand-kept against `internal/model`, now the
one declaration they mirror (ADR 036); generating them from it instead of
hand-mirroring is WL-76, not yet done.

## Specs, plans, tasks

The model (spec 025; files under `docs/` are the transitional mirror until it
is implemented):

- A **spec** is a durable document. Writing or revising one is an ordinary
  claimable task (`kind = 'design'`, renamed from `spec` by 025 §10, which also
  widens its meaning to any design document — spec, ADR, or plan) that closes on
  submission for review, not on document acceptance, which is a status
  transition on the document rather than a task state. "Is the spec
  implemented?" is a coverage query, never a task state — do not create
  long-lived umbrella tasks per spec.
- A **plan** is an executable document; its execution is the set of tasks
  minted when the plan is accepted. 025 §9.2 mints no root row above them and
  groups them by a reference to the plan document instead. No kind declares a
  container: container-ness is inferred from a task's `child_of` children
  (004 §6.1). Do not create free-standing container tasks.
- **Groupings are queries, not rows** (025 §1): one plan's tasks = the tasks
  referencing it; cross-plan "ships together" = Milestone over Deliverables
  (v2); everything in a repo set = the project. There is no sprint concept
  and no container above a plan's tasks — order plans with `blocks`.
- Spec → plan decomposition is always an explicit human act; skills may
  offer it, never perform it unasked.

`e2e/` drives the stack through public surfaces only (HTTP API, signed
webhooks, web pages) — never direct store writes. Keep it that way; it exists
to prove the real user path works.

`www/` is the static marketing site: own deploy workflow, shares no code with
the Go build. Docs-only PRs (only `*.md`, `docs/`, `www/`) skip CI checks;
the `can-be-tested` label forces a run. `docs/specs/`, `docs/plans/` and
`plugins/` are exempt from that skip — their markdown is input, not prose.
The `obsidian` job is the one check scoped to a subtree: it runs only when a PR
touches `obsidian/` or `_obsidian.yml`, decided by a `gate` output rather than a
`paths:` filter, because a reusable workflow cannot take one. `can-be-tested`
does not force it — that label authorises CI, it does not make an untouched
subtree worth rebuilding.

## The lode plugin

`plugins/lode/` is the agent-facing half of this repo: the `/lode:*` task
pickup surface and the `lode-worker` agent. It lives here so it versions with
the binary it drives — the lifecycle hooks it used to carry now ship with the
CLI (`lode install`). There is no `commands/` directory: every `/lode:*` entry
point is a skill under `plugins/lode/skills/`. `next`, `resume`, `done`,
`block` and `status` set `disable-model-invocation: true`, so they are
reachable only as the slash commands `/lode:next` and friends;
`working-under-worklode` stays model-invocable — it is the done/block/release
judgment loop a worktree session loads on its own. This repo is its own
marketplace, named `worklode`:

```
/plugin marketplace add sunstoneinstitute/worklode
/plugin install lode@worklode
```

`.claude/settings.json` enables `lode@worklode` for this repo, but enabling is
not installing: a fresh checkout still needs the install above — and a session
restart after it — before any `/lode:*` command exists.

**The Claude JSON is the source of truth.** Edit
`.claude-plugin/marketplace.json`; never hand-edit `.agents/plugins/marketplace.json`
or any `.codex-plugin/plugin.json`. Those are generated:

```bash
./scripts/sync-codex-marketplace.py          # regenerate
./scripts/sync-codex-marketplace.py --check  # what pre-commit and CI run
```

The generator strips the leading Claude surface tag (`[code]`) from Codex
descriptions and adds Codex interface and installation metadata. Schema
validation is deliberately not vendored — it needs the third-party
`jsonschema` package, and `--check` plus `claude plugin validate .` cover the
ground without adding a Python stack to a Go repo.

## Conventions

- Tokens are `wl_` + 40 hex chars everywhere (bootstrap, minted, e2e
  fixtures); anything else is treated as a token hash, not a plaintext.
- Task branches are rendered from `LODE_BRANCH_TEMPLATE` (default
  `{{ .id }}-{{ .slug }}`, e.g. `WL-7-fix-the-thing`); the server is the
  authority on branch names. Worktrees live under `worktree_dir` (default
  `.worktrees`), configurable per repo in `.worklode/config.toml`.
  `LODE_WORKTREE_DIR` overrides it client-side for one-off/CI use, but it
  doesn't persist — a session started without it won't recognise worktrees
  created with it set, so `worktree_dir` is the durable setting.
- A worktree's task id is read via `worktree.Layout.TaskID`
  (`internal/worktree`): the explicit `worklode.task-id` git config `lode next`
  stamps on creation (`worktree.SetTaskID`), falling back to the id in the
  directory name (`Layout.ParseDir`) for worktrees created before this field
  existed. Both share the same guard — the path must be exactly one level
  below the base — so only id resolution costs a git subprocess (008 §5.2).
  `git config --worktree` needs `extensions.worktreeConfig` enabled in the
  repo's own local config — global config does not count — which `lode
  install` sets per-repo; `lode next` also sets it defensively before
  creating a worktree.
- **Integrating a diverged `main` is a rebase, never a merge.** When local
  `main` is ahead and `origin/main` has also moved, `git pull --rebase` before
  pushing; a merge commit created only to absorb remote commits is noise in the
  trunk. Rebasing rewrites the SHA of every replayed commit, so check first
  whether an open PR's head sits on one of them (`gh pr list --state open`) —
  that PR goes stale with an empty diff instead of landing. Say so and let the
  author decide; never quietly substitute a merge.
- `MODEL_SELECTION.md` defines which Claude Code or Codex model tier and
  reasoning effort each agent role uses when working this repo with subagents.
- **Every file you create under `docs/specs/` or `docs/plans/` starts with YAML
  frontmatter — no exceptions.** A spec needs `status` and, once accepted,
  `issued`. A plan needs `status` and `covers` — the spec sections it undertakes
  to see built, optionally qualified by a `coverage:` level (026 §5). It is
  `covers` rather than `implements` because a plan writes no code: `implements`
  is a component's claim that its code meets a section, and one word for the
  promise and for the evidence leaves them indistinguishable. `implements` still
  parses on a plan and is reported as retired.
  When no spec governs it, write `covers: NO-SPEC` (the reserved "no governing
  spec" sentinel, which takes no project key — 026 §4.3) rather than omitting
  the key, because an absent `covers` is
  indistinguishable from a forgotten one. Frontmatter keys are ontology
  property names, ordered lifecycle → `covers` → dependency → amendment →
  supersession. `scripts/secmeta.py` checks all of this on commit; it reports
  and never rewrites, so a failure is yours to decide, not to re-run.
- Spec sections carry `{#sec-N}` anchors that are **frozen once the spec is
  accepted** — amend or supersede a section, never renumber it. Before creating
  or editing anything in `docs/specs/` or `docs/plans/`, read
  `docs/authoring-design-docs.md`: filenames, the frontmatter schema, and how
  to amend/supersede. `scripts/secfmt.py` enforces the numbering (pre-commit
  hook; docs-only PRs skip CI, so the hooks are the real gate).
- `ns/` holds the `wl:` ontology extracted from specs 006/016/025/026:
  `ontology.ttl` (classes, properties, axioms), `concept.ttl` (SKOS enums),
  `shapes.ttl` (SHACL). It is the vocabulary the frontmatter keys come from, and
  the parseable form — the specs' own Turtle blocks are illustrative and do not
  parse. `ns/` owns the shared schema and the specs own the rationale
  (025 §17); until the codegen step exists, amend the spec first, then mirror
  the term here (`riot --validate ns/*.ttl`) — and never edit `wlc:TaskKind`
  apart from the migration and `validKinds`, which a test holds together.
- Term names in `ns/*.ttl` are camelCase: `wl:` properties lowerCamelCase
  (`wl:coveringPlan`, `wl:runtimeEventKind`), classes and concept schemes
  UpperCamelCase (`wl:DesignDoc`, `wlc:TaskKind`). Snake_case is reserved for
  `wlc:` concepts that carry a stored enum value — `wlc:docker_image` spells the
  `docker_image` in the CHECK constraint — so a term's spelling says whether it
  names schema or data.

## Metrics

Server-side changes that add an HTTP endpoint, background loop, outbound
call, or store operation with meaningful outcomes must add or extend
`worklode_*` Prometheus metrics in the owning package, with tests. Follow the
conventions in `docs/specs/022-prometheus-metrics.md`: nil-safe metrics
struct in the owning package's `metrics.go`, `prometheus.Registerer`
threaded from `serve.go`, bounded label values, `worklode_` prefix.
