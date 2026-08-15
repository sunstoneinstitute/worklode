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
read-only web UI. See `docs/specs/` for the design — `docs/specs/index.yaml`
maps every document's sections, and `004-execution-backbone.md` is the
foundation the rest builds on.

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

Or via a package manager:

- macOS (Homebrew): `brew install sunstoneinstitute/tap/worklode`
- Windows (Scoop): `scoop bucket add sunstone https://github.com/sunstoneinstitute/scoop-bucket && scoop install sunstone/worklode`

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

### Project scoping

Commands that act on a set of tasks — `lode task list`, `lode task add`,
`lode task claim --next`, `lode next`, `lode board`, `lode inbox list` — scope
themselves to the project of the repo you are in. The project is resolved in
this order, first hit wins:

1. `--project <id>` or `--repo <owner/name>` on the command line.
   `--project=` (explicitly empty) means *all projects*.
2. `current_project` in the repo's `.worklode/config.toml` (or `.lode/`),
   found by walking up from the working directory.
3. `current_project` in `~/.config/worklode/config.toml`.
4. The repo's `origin` git remote, resolved against the repo → project
   mappings created by `lode project add-repo`.
5. Nothing — the command runs across every project.

Step 4 needs no setup beyond the mapping already on the server, so a fresh
clone or a new worktree is scoped correctly on the first command. Its answer
is cached in `~/.cache/worklode/remotes.json` for a week (an unmapped repo for
an hour), so it costs one request per repo, not one per command. Anything that
goes wrong there — no remote, an unreachable server, an unmapped repo — falls
through to step 5 rather than failing the command.

To see what the current directory resolves to:

```bash
lode project resolve
# worklode (WL) — from git remote git@github.com:sunstoneinstitute/worklode.git (cached)

lode project resolve --refresh   # re-query the server
```

To pin a project explicitly, set it in `.worklode/config.toml` at the repo
root:

```toml
current_project = "sunstone-web"
project_key = "WL"   # design-doc shorthand key for this repo, e.g. WL-SPEC-25
```

```bash
lode task add --title "Fix the footer link"   # goes to sunstone-web
lode task list --project=                     # opt back out to all projects
lode board --repo sunstoneinstitute/other     # name a project by its repo
```

Inside a scoped repo, commands that take a task id also take a bare task
number: `lode task show 12` means `WL-12`. Full ids work everywhere.

### Importing an existing backlog

`add-repo` only wires up new webhook traffic — issues and PRs that predate
the mapping stay invisible until backfilled:

```bash
lode project add-repo myproject acme/widgets
lode inbox import acme/widgets --dry-run
lode inbox import acme/widgets
lode task add --project myproject --kind chore --title "acme/widgets backlog" --priority medium
lode inbox promote acme/widgets 41 --priority medium --draft --parent <backlog-id>
```

- `lode inbox import <repo>` backfills through the same store path the
  webhooks use — it never drives the delivery lifecycle, so re-running is
  always safe. It defaults to open issues; add `--include-prs`, `--state
  closed`/`--state all`, or `--since <RFC3339>` to widen it. It caps at 20
  pages of 100 per kind, and on truncation prints the `--since` value to
  resume with.
- `--draft` on `lode inbox promote` lands the task in `draft` (not claimable
  until `lode task ready`); `--parent <id>` files it under an existing task in
  the same step. Any ordinary task can be a parent — the `child_of` edge is
  what makes it a container (spec 004 §6.1).
- `lode inbox link <repo> <number> <task-id>` marks an issue as already
  covered by an existing task, without creating a new one.

The CLI merges the repo config over `~/.config/worklode/config.toml`. It may
set `server` and `current_project`, but not `token` — repo configs tend to be
committed, and the token belongs in the OS keychain (or `LODE_TOKEN`).

The read-only web UI is at http://localhost:8080/.

## Network exposure

The compose file publishes port 8080 on loopback only
(`"127.0.0.1:8080:8080"`). Keep it that way on shared hosts: the web board
is unauthenticated *because* the compose file sets `LODE_WEB_OPEN=1`, and
`/metrics` is unauthenticated by construction (bearer tokens cover `/api/v1`
only), so anyone who can reach the port can read every task. Compose sets the
flag for you — following the quickstart verbatim needs no extra step, and no
export to add.

A server with neither `LODE_WEB_OPEN` nor `LODE_OIDC_*` refuses to serve any
web page at all: `503`, naming the missing configuration. That is the default
outside compose, and it is deliberate — accidental openness is the hazard, so
serving anonymously is an explicit choice.

To serve other machines, configure `LODE_OIDC_ISSUER` and
`LODE_OIDC_CLIENT_ID` and drop `LODE_WEB_OPEN` rather than only widening the
mapping to `"8080:8080"`; then put a TLS-terminating reverse proxy or
firewall in front.

## GitHub App setup

Create a GitHub App on the `sunstoneinstitute` org and install it org-wide
(all repos). Configure:

- **Webhook URL**: `https://<host>/hooks/github`
- **Webhook secret**: a random string, set as `LODE_GITHUB_WEBHOOK_SECRET` on
  the server
- **Subscribe to events**: Issues, Pull requests, Pull request reviews,
  Workflow runs, Releases, Pushes, Deployment statuses
- **Repository permissions**: Contents: read (pushes, releases), Deployments:
  read (deployment statuses), Actions: read (environment discovery). Without
  Actions: read, repos stay at `done_state = merged` and tasks stop advancing
  there.
- **App credentials**: set `LODE_GITHUB_APP_ID` and
  `LODE_GITHUB_APP_PRIVATE_KEY` (the PEM) on the server so it can mint
  installation tokens for repo discovery.

For local testing without a public URL, forward deliveries from a real repo:

```bash
gh webhook forward --repo=sunstoneinstitute/<repo> \
  --events=issues,pull_request,pull_request_review,workflow_run,release,push,deployment_status \
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
`LODE_CLUSTER_ENV_MAP="hzprod=prod,hzdev=dev"`. Only `dev` and `prod` are
valid environments — any other value is a startup error, since delivery
tracking has no other stage. A cluster missing from the map falls back to
`dev`.

## Task branches

Task branches are rendered from `LODE_BRANCH_TEMPLATE` on the server (default
`{{ .id }}-{{ .slug }}`, e.g. `WL-7-fix-the-thing`). The server is the
authority: `lode next` and `lode task claim` use the branch the claim response
returns. Worktrees live under `worktree_dir` (default `.worktrees`),
configurable per repo in `.worklode/config.toml`. `LODE_WORKTREE_DIR` overrides
`worktree_dir` on the client, but it is for one-off and CI use only: it does
not persist anywhere, so a later session started without it will not
recognise worktrees created with it set. Set `worktree_dir` in the repo config
instead for anything durable.

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

`lode login` opens a browser and completes over a loopback port. Where it
cannot — no opener installed, or no display, as over SSH — it prints a URL to
open on any machine and waits for the one-time code that page shows you to be
pasted back. `--no-browser` asks for that directly.

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

## Org skills

`lode skills` manages the org-wide agent skill registry, synced from git
source repos named in `LODE_SKILL_SOURCES` (comma-separated
`owner/repo@ref:glob` entries, e.g.
`sunstoneinstitute/claude-plugins@main:plugins/*/skills/*`; requires the
GitHub App):

- `lode skills sync` — trigger a full server-side re-sync (admin).
- `lode skills list` — list skills known to the server.
- `lode skills recommend` — cosine-similarity matches for a task or free text.
- `lode skills install <name>[@<hash>]` — fetch a skill into the local
  content-addressed store (`~/.worklode/skills`).

Pin skills to a task with `lode task add --skill <name>` (repeatable) or
manage them after the fact with `lode task skills <id> [--set ...]`; pinned
skills are always inlined in `lode task brief`.

Recommendations need `LODE_EMBEDDING_URL` and `LODE_EMBEDDING_MODEL` on the
server, and, if the endpoint requires auth, `LODE_EMBEDDING_API_KEY`. With
`LODE_EMBEDDING_URL` unset, the server runs pins-only: briefs and
recommendations both still work, just without similarity matches.

## Worklode plugin (Claude Code)

Installing the `lode` CLI is covered in Quickstart above; this section covers
the agent-facing pickup workflow built on top of it.

Run `lode install` inside a repo to install both integrations at once: three
git hooks and the Claude Code hook bindings described below. It is idempotent —
safe to re-run.

| git hook | What it does |
|---|---|
| `pre-commit` | Heartbeat: renews the current task's lease on every commit. |
| `post-merge` | Reports a merge that lands on the default branch here, so the task advances without waiting for a GitHub webhook. |
| `post-commit` | The same reporter, for the squash merges and conflict-resolving commits `post-merge` never sees. |

Each chains whatever hook was already installed on the same event; `pre-commit`
additionally chains the `pre-commit` framework when the repo has a
`.pre-commit-config.yaml`.

```bash
lode install                     # git hooks + Claude Code bindings
lode install --no-agent          # git hooks only
lode install --no-vcs            # Claude Code bindings only
```

The merge reporters are a NOP unless HEAD is the repo's default branch, which
is never true inside a worktree — so an ordinary commit on a task branch costs
one local `git` call and no network.

`--vcs` defaults to `git` and `--agent` to `claude-code`; those are the only
supported values today, and the flags exist so another VCS or agent can be added
without changing the CLI shape.

### Agent session tracking

`lode hook` reports which coding-agent session is working a task, so the
backbone can show what is running right now. Sessions are recorded against the
task's lease; a lease outlives many sessions (restarts, `/clear`, resuming the
next day), and one session can span several leases as it moves between
worktrees.

The reporting agent comes from `LODE_AGENT`, defaulting to `claude-code`.
Accepted values: `claude-code`, `codex`, `cursor`, `aider`, `opencode`, `pi`,
`amp`, `other`.

`lode hook --list` prints every supported event, what `lode install` binds it
to, and what its handler does.

Claude Code bindings:

| `lode hook` event | Claude Code event |
|---|---|
| `session-start` | `SessionStart` |
| `heartbeat` | `Stop`, `StopFailure`, `SubagentStop`, `Notification` |
| `worktree-enter` | `PostToolUse` matcher `EnterWorktree` |
| `session-end` | `SessionEnd` |

`worktree-create` and `worktree-remove` are not bound to Claude Code.
`WorktreeCreate`/`WorktreeRemove` are delegation hooks: binding one makes it
*the* worktree creator, replacing Claude Code's built-in `git worktree add`, and
`EnterWorktree` then fails unless the hook prints the path it created. Both
events stay available for scripts that do create Worklode's own worktrees
(under `.worktrees/` by default); invoke them as `lode hook worktree-create` /
`worktree-remove`.

Install these bindings into a repo with:

```
lode install                           # .claude/settings.local.json
lode install --scope project           # .claude/settings.json
```

`lode uninstall` (same flags) removes both integrations again: it restores
whatever git hooks Worklode preserved and strips every `lode hook` entry
from the settings file. Both commands are idempotent, the VCS side never touches
a hook it does not recognize as its own, and the agent side only
touches entries whose command starts with `lode hook`, so third-party hooks on
the same events are left alone.

Heartbeats are debounced to one per minute per worktree, so binding `Stop` is
cheap even in a fast conversation. Every hook stays inside the 2s backbone
timeout and never fails the event that triggered it.

`worktree-exit` has no Claude Code binding: `ExitWorktree` reports no path, and
by the time the hook fires the session's directory has already been restored to
the one being returned to — so acting on it would close the wrong session. It
requires an explicit path in `tool_input` and is a NOP without one. A session
that leaves a worktree ages out instead: its `last_seen_at` stops advancing, and
the row is closed for good when the lease is released, expires, or the task
completes.

### Token cost

Ending a session reports what it spent. `lode hook` parses the agent's own
transcript — Claude Code's `SessionEnd` payload carries `transcript_path`, and
every assistant entry in it carries the vendor's `usage` block, so the numbers
are reported rather than estimated. The server prices them from `model_prices`.

A prompt is not one number. It bills as four separate classes, at rates that
span a factor of twenty:

| Class | Rate vs. base input | What it is |
|---|---|---|
| `input_tokens` | 1x | the uncached remainder of the prompt — **not** the prompt size |
| `cache_creation` 5m TTL | 1.25x | prefix written to cache |
| `cache_creation` 1h TTL | 2x | same, at the longer TTL |
| `cache_read_input_tokens` | 0.1x | prefix served from cache |

Output is billed separately and is never cached; last turn's output re-enters
the next prompt and is billed as a cache read from then on.

The classes are not interchangeable. On one real session of this repo — 1.9k
uncached input, 354k cache writes, 11.8M cache reads, 58k output — the correct
figure is **$10.88**. Pricing every input token at the base rate gives $62.12;
using only `input_tokens` and `output_tokens` gives $1.45.

Two more things the numbers depend on: usage is recorded per model, because one
session mixes them (a main loop on one, subagents on another) at several-fold
different rates; and transcript entries are deduplicated by message id, since
an assistant message is written once per content block with the whole usage
block repeated on each line.

Rates live in `model_prices` and are effective-dated, so a past session keeps
pricing at the rate that applied when it ran. Correcting a rate or filing one
ahead of its date is a row, not a redeploy. A model with no rate on file is
reported as unpriced rather than billed at zero.

```
lode project show                 # current project, last 30 days
lode project show --project wl --days 7
lode project show --days 0        # all history
```

The Claude Code plugin (`lode` plugin, `plugins/lode/` in the
`sunstoneinstitute/claude-plugins` repo, installable from the Sunstone
plugins marketplace) provides a `/lode:*` slash-command flow for agents
picking up work:

- `/lode:next` — claim the next ready task, create its `.worktrees/<id>-<slug>`
  git worktree, bind the lease to it, and start from the injected task brief.
- `/lode:resume` — re-acquire the task already bound to the current worktree.
- `/lode:done` — mark the task done, release the lease, and print a
  worktree-cleanup hint.
- `/lode:block --on <id>` — record a real blocker on another task and
  release the lease.
- `/lode:status` — read-only report of the current task, lease, and
  heartbeat state.

These are thin wrappers over the underlying `lode` subcommands: `lode next`,
`lode resume`, `lode done`, `lode block`, `lode status`, and
`lode task brief <id>`.

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

### Graph-server acceptance (spec 006 §13)

`e2e/graphserver_test.go` proves the knowledge-graph hand-off end-to-end
against a live data-platform graph-server: Keycloak client-credentials
auth, a named-graph PUT to the fixed `main` branch, a GSP read-back, and a
drift query over `/sparql`. It skips unless configured:

```bash
export LODE_GRAPHSERVER_URL=https://graph.dev.sunstoneinstitute.ai
export LODE_GRAPHSERVER_TOKEN_URL=https://auth.sunstoneinstitute.ai/realms/sunstone/protocol/openid-connect/token
export LODE_GRAPHSERVER_CLIENT_ID=dataplatform-svc
export LODE_GRAPHSERVER_CLIENT_SECRET="$(op item get dataplatform-svc --fields credential --reveal)"
go test -tags e2e ./e2e/ -run TestGraphServerAcceptance -v
```

Each run writes one uniquely-named graph and deletes it afterwards, so runs
never collide; point `LODE_GRAPHSERVER_URL` at prod to re-certify after a
graph-server deploy, at the cost of two extra commits in prod's Nessie history
per run (the write and the cleanup delete, both on `main`). The three
`LODE_GRAPHSERVER_*` credential variables must be set together or not at all;
with none of them set, the client runs unauthenticated, which only works
against a graph-server started without `AUTH_ENFORCE`. The client behind it
lives in `internal/graphserver`; the manual equivalent is the data-platform
runbook `docs/runbooks/2026-07-22-worklode-projector-acceptance.md`.

### CI gate (who may run PR checks)

`pr-checks.yml` opens with a cheap `gate` job; the lint/test/build/kustomize
jobs `needs: gate` and run only when `gate.outputs.run == 'true'`. A PR runs
the checks when its author is the repo owner, an org member, or an invited
collaborator (GitHub's `author_association`), or when a maintainer has applied
the `can-be-tested` label. Applying labels needs Triage+ on the repo, so an
outside contributor cannot self-authorise; the workflow listens for the
`labeled` PR event so adding the label re-triggers the run.

The gate also skips the checks for **docs-only PRs** — every changed file
markdown (`*.md`) or under `docs/`. The `can-be-tested` label overrides this.
Jobs skipped via `if:` count as satisfied for branch-protection required
checks, so a skipped run does not block merging.

## License

MIT — see [LICENSE](LICENSE).
