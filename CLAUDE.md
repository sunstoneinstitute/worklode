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
specs have amended it since. `docs/specs/` stays the corpus of record — **edit
there, never in `inlined/`** — and a pre-commit hook regenerates the views.
Implementation plans live in `docs/plans/`; `docs/follow-ups.md` holds known
non-blocking gaps — check it before filing something as new.

## Where the rest of the guidance lives

Each area below has a skill that carries its detail; it fires on the work
itself, but load it by name if it has not:

- **Writing or editing anything under `docs/specs/` or `docs/plans/`** —
  frontmatter, `covers:`, `{#sec-N}` anchors, amend/supersede, the `ns/`
  `wl:` ontology and its camelCase term naming, and the spec/plan/task model
  (what is a claimable task vs a document status). See
  the `worklode-docs-authoring` skill.
- **Adding or changing a database migration** under `deploy/base/migrations/`.
  See the `worklode-migrations` skill.
- **Touching `plugins/obsidian/`** — the TypeScript Obsidian plugin, its pnpm
  toolchain, and its hand-kept wire types. See the
  `worklode-obsidian-mirror` skill.
- **Touching `plugins/claude/lode/` or its marketplace mirrors** — the
  `/lode:*` slash commands, the `lode-worker` agent, the marketplace, and the
  generated Codex mirror. See
  the `worklode-lode-plugin` skill.
- **Working on the cockpit UI** — `internal/ui`, `templ` components, the
  Tailwind build, the `go generate` loop. See the `worklode-cockpit-ui` skill.
- **Changing CI, workflows, or `www/`** — the docs-only skip, the
  `can-be-tested` label, the subtree-scoped `obsidian` job. See
  the `worklode-ci` skill. Editing the site's own copy is a separate
  concern from its CI: see `www/CLAUDE.md` for the language style and the
  accuracy bar its content is held to.
- **Changing the CLI in `internal/cmd`** — every skill above, both plugin
  marketplaces, and the org onboarding skill in another repo hardcode `lode`
  invocations. `docs/agent-surfaces.md` is the register of those surfaces and
  the checklist for keeping them true; it also holds the rules for adding and
  retiring a skill.

## Commands

Prefer the `Makefile` targets over bare `go build`/`go test` — they build and
test with `-trimpath`, matching what CI and the release Dockerfiles do, so a
local pass means the same thing a CI pass does. `-trimpath` also strips the
absolute source path from the build id, so every worktree under `.worktrees/`
shares one build cache instead of recompiling the world per worktree:

```bash
make build      # go build -trimpath -o bin/lode ./cmd/lode
make install    # build and install lode to /usr/local/bin
make test       # go test -trimpath -race -count=1 ./...
make test-e2e   # e2e suite (build tag required, TEST_POSTGRES_DSN reachable)
make vet        # go vet ./...
```

For anything a Makefile target doesn't cover — a single test, one package —
fall back to `go build`/`go test` directly and add `-trimpath` yourself — **never
run either bare** — for both reasons above:

```bash
go test -trimpath ./internal/store -run TestClaim   # single test
./scripts/check-migrations.sh --no-fix    # migration-number collision check
./scripts/secfmt.py -l              # spec section numbering + anchor check
./scripts/inlinespec.py             # regenerate docs/specs/inlined/
./scripts/nsgen.py                  # regenerate internal/ns/gen.go from ns/concept.ttl
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

## Architecture

Request flow: `internal/cmd` (cobra commands, both server and client sides) →
`internal/cli` (HTTP client, project scoping, rendering) → `internal/api`
(HTTP server: `/api/v1` bearer-token API, a session-gated, read-mostly project
cockpit, `/metrics`, OIDC login) → `internal/store` (all Postgres access via
pgx; domain logic like task state machine, ranking, atomic `claim` live here).
The cockpit's web pages render with `templ` components in `internal/ui`, which
depends on nothing beyond stdlib, `internal/model`, and the templ runtime —
`internal/api` imports `internal/ui`, never the reverse.

**`internal/cmd` decides, `internal/cli` renders.** The seam is "does it know
what cobra is", and it holds in both directions. `internal/cli` never imports
cobra — that is what lets `internal/hookrun` and `internal/worktree` reuse it.
Going the other way: every human-readable view of an `internal/model` shape —
list *or* detail — is a `cli.*Table`/`cli.*Render` function in
`internal/cli/render.go` taking an `io.Writer`, and a cobra `RunE` fetches,
picks `--json` or human, and calls exactly one of them. So nothing under
`internal/cmd` builds a `tabwriter` or formats a timestamp itself; a one-line
confirmation that needs a time uses `cli.LocalTime`, and cell formatters
(`cli.Money`, `cli.HumanTokens`, `cli.DocNumber`, `cli.KeySuffix`) are shared,
never re-derived. What legitimately renders in `internal/cmd` is output over
values that never cross the API: `lode doc import`'s dry run over walked corpus
files, `lode hook`'s list of hook names, `lode next`'s no-work guidance.
`internal/cmd/renderrule_test.go` catches the two tells a view has drifted back
— a hand-built tabwriter, a hand-formatted timestamp — but it is a tripwire,
not the rule; this paragraph is.

**One model, not one per package (ADR 036).** Every shape that crosses the
HTTP boundary — entities, response projections, request bodies — is declared
once in `internal/model` (stdlib imports only). `internal/store` scans into
it, `internal/api` serializes it, `internal/cli` decodes it, `internal/ui`
embeds it. Field names are wire names (`Project`, not `ProjectID`). Only three
kinds of type stay package-local: `internal/ui` view types, `internal/store`
scan plumbing, and `internal/api` transport internals (`Subject`, sessions,
route guards). Anything else needs an amendment to 036, not a new struct.
`internal/model/rule_test.go` enforces this and names what it rejects;
`deps_test.go` keeps the package a stdlib-only leaf. Read the failure message
before working around either.

Every route is guarded through one table. `internal/api/router.go`'s
`routeGuards` names the permission each route requires — or `open("why")` when
it deliberately needs no worklode identity — and `NewServer` refuses to boot on
a route the table does not name or a table entry no route uses, so a new
endpoint cannot ship unguarded. `internal/api/authz.go` holds the policy: a
per-request `Subject`, a `grants` table of permission → roles, and a
default-deny `Decide`. There is no RBAC model yet — the two roles are the
`user`/`admin` Keycloak already syncs (001 §9.2) — so add real roles by editing
that table, never by adding a check inside a handler.

Ingest paths write through the same store layer: `internal/hooks` (GitHub App
and Flux webhooks, both HMAC-signed), `internal/watch` (pod informer for crash
loops/OOM kills), and `lode inbox import` (backfill through the webhook store
path, so re-running is safe).

Cross-cutting pieces: `internal/gitexec` (every `git` subprocess in the
binary, so environment policy and error shape live in one place — a guard
test fails the build on a direct `exec.Command("git", ...)` anywhere else),
the files `lode install` manages — git hooks in `internal/githooks`, agent
settings in `internal/harness` — neither of which `internal/cmd` reimplements,
worktree-bound leases (`internal/worktree`,
`internal/hookrun`), agent-session tracking priced from the agent's own
transcript (`internal/transcript`, `store/pricing` — rates are effective-dated
rows in `model_prices`, never hardcoded), the org skill registry with pgvector
embeddings (`internal/skillsync`, `skillstore`), and `internal/eventbus`
(offset-tracked subscribers over the events log, read via `lode event tail
--follow`). Its one subscriber, `doc-lifecycle`, mints the review and planning
tasks a document's lifecycle calls for (025 §15.4): the rules are a pure
function in `internal/watcher`, the executor that feeds them is
`internal/api/docwatch.go`, and `NewServer` starts the loop only when the
caller passes a `BackgroundCtx`.

The backbone (this repo, Postgres) owns execution facts and — once spec 025 is
implemented — design-document artifacts; derived architecture facts and the
queryable view of both belong to the data-platform knowledge graph (specs
006/025), which receives documents by projection. No fact has two owners —
keep new state on the right side of that split.

## Conventions

- Tokens are `wl_` + 40 hex chars everywhere (bootstrap, minted, e2e
  fixtures); anything else is treated as a token hash, not a plaintext.
- Task branches are rendered from `LODE_BRANCH_TEMPLATE` (default
  `{{ .id }}-{{ .slug }}`, e.g. `WL-7-fix-the-thing`); the server is the
  authority on branch names. Worktrees live under `worktree_dir` (default
  `.worktrees`), configurable per repo in `.worklode/config.toml`.
  `LODE_WORKTREE_DIR` overrides it client-side for one-off/CI use, but it
  doesn't persist, so `worktree_dir` is the durable setting. A worktree's task
  id is read via `worktree.Layout.TaskID` (`internal/worktree`), which needs
  `extensions.worktreeConfig` in the repo's own local config — `lode install`
  and `lode next` both set it.
- **Integrating a diverged `main` is a rebase, never a merge.** When local
  `main` is ahead and `origin/main` has also moved, `git pull --rebase` before
  pushing; a merge commit created only to absorb remote commits is noise in the
  trunk. Rebasing rewrites the SHA of every replayed commit, so check first
  whether an open PR's head sits on one of them (`gh pr list --state open`) —
  that PR goes stale with an empty diff instead of landing. Say so and let the
  author decide; never quietly substitute a merge.
- `MODEL_SELECTION.md` defines which Claude Code or Codex model tier and
  reasoning effort each agent role uses when working this repo with subagents.
- `e2e/` drives the stack through public surfaces only (HTTP API, signed
  webhooks, web pages) — never direct store writes. Keep it that way.

## Metrics

Server-side changes that add an HTTP endpoint, background loop, outbound
call, or store operation with meaningful outcomes must add or extend
`worklode_*` Prometheus metrics in the owning package, with tests. Follow the
conventions in `docs/specs/022-prometheus-metrics.md`: nil-safe metrics
struct in the owning package's `metrics.go`, `prometheus.Registerer`
threaded from `serve.go`, bounded label values, `worklode_` prefix.
