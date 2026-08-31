# Self-hosted CI runner (hel01)

`test`, `lint` and `build-image` in `pr-checks.yml` run on a self-hosted
GitHub Actions runner on `hel01` when the triggering PR is trustworthy;
`validate-kustomize`, `obsidian`, and every PR the gate doesn't trust stay on
`ubuntu-latest`. `deploy-dev.yml`'s `build-image` targets hel01
unconditionally — a push to `main` has no fork head, so it is trusted by
construction and never needs the gate. The payoff is a warm, persistent
`GOCACHE`/`GOMODCACHE` — no `actions/cache` round trip for Go, no re-fetching
`go.sum` on every run — plus 48 cores and a persistent Docker build cache
instead of a hosted runner's 4 cores and a from-scratch layer cache each
time.

## Trust boundary

`worklode` is a **public** repo, and a self-hosted runner executes arbitrary
job code with the runner user's access to the host. `pr-checks.yml`'s `gate`
job computes a `trusted` output — true only when the PR's head repo is this
repo (`github.event.pull_request.head.repo.full_name ==
github.repository`), independent of `run` (which the `can-be-tested` label
or author association can also set true). **A fork PR never gets `trusted`,
regardless of label or association** — it always runs on `ubuntu-latest`.
`lint`/`test`/`build-image` route their `runs-on` input off `trusted`, not
`run`.

Do not widen `trusted` to include forks from collaborators/members: hosted
CI already accepts that risk for a throwaway VM per job; hel01 is not
throwaway, and it holds this machine's SSH keys, kubeconfig, and other
credentials outside the runner user's reach only because of the isolation
below.

## Isolation

The runner runs as a dedicated **system user, `ghrunner`** — no `sudo`, no
login shell used interactively, home `/home/ghrunner` mode `0700`. It cannot
read `~stig` and cannot `sudo`.

**It *is* in the `docker` group**, added specifically so `build-image` can
reach the Docker socket. That is a materially weaker boundary than the rest
of this setup: a user who can talk to the Docker socket can run a container
that bind-mounts the host root and is, for practical purposes, root on
hel01. Docker-group membership is root-equivalent — there is no
Docker-socket ACL that stops short of that. Weigh that against `trusted`
above before adding another job here: the whole isolation model now rests on
`trusted` being right, not on `ghrunner` being unprivileged.

Registered as two repo-level runners (not org-level), `hel01` and `hel01-2`,
both carrying labels `self-hosted, Linux, X64, hel01, gha-pgvector, docker,
gha-buildcache`. `lint` targets `self-hosted` and `gha-buildcache`; `test`
targets `gha-pgvector` and `gha-buildcache`; `build-image` targets `docker`
and `gha-buildcache` — see the sections below for why each label exists
rather than every job sharing `self-hosted`. Either runner can pick up any
of the three jobs; GitHub schedules to whichever is idle.

Installed as systemd services:

```
systemctl status actions.runner.sunstoneinstitute-worklode.hel01.service
systemctl status actions.runner.sunstoneinstitute-worklode.hel01-2.service
```

Reinstall/reconfigure `hel01` from `/home/ghrunner/actions-runner`
(`config.sh`, `svc.sh`) per GitHub's own self-hosted runner docs;
`hel01-2` the same way from `/home/ghrunner/actions-runner2`. A fresh
registration token for either comes from `gh api -X POST
repos/sunstoneinstitute/worklode/actions/runners/registration-token`. A
group-membership change (like the docker group above) needs both services
restarted — `usermod` alone doesn't affect an already-running process:

```
sudo systemctl restart actions.runner.sunstoneinstitute-worklode.hel01.service
sudo systemctl restart actions.runner.sunstoneinstitute-worklode.hel01-2.service
```

### Two executors, one user, separate caches

Both runners execute as the same `ghrunner` system user — a second
dedicated user would double every isolation fact in this doc (docker-group
grant, home permissions) for no security benefit, since both processes
already run at the same privilege. What has to differ is the **cache
state** Go resolves from `$HOME`: while Go's own build/module cache format
tolerates concurrent access, there's no reason to make `test`/`lint` share
one. `build-image`'s cache is a Docker volume behind a shared named builder,
which both runners use by design — BuildKit handles concurrent builds itself.

`hel01-2`'s systemd unit sets `Environment=HOME=/home/ghrunner/runner2-home`
(everything else — `User=ghrunner`, working directory under
`actions-runner2`, docker-group membership — matches `hel01`). That single
override redirects `go env GOCACHE`/`GOMODCACHE` onto a second, cold cache
tree, so the two runners can run any combination of jobs concurrently without
touching each other's files. The `gha-ci-postgres` container below is
unaffected — it's reached over TCP, not through `$HOME`, so both runners
already share it safely by the same per-test-database convention. Setting up
a third runner needs the same two things: its own `actions-runner<n>` install
directory, and a `runner<n>-home` directory named in its unit's
`Environment=HOME=`.

## Postgres for `test`

Store tests need a reachable Postgres with pgvector. `ubuntu-latest` keeps
the existing `services:` block (ephemeral, per-job). The self-hosted path
instead points at an **always-on container** on hel01, independent of the
runner user (no docker access needed to reach it — it's just a TCP port):

```
docker run -d --name gha-ci-postgres --restart=always \
  -p 127.0.0.1:15432:5432 \
  -e POSTGRES_PASSWORD=postgres \
  --health-cmd='pg_isready -U postgres' --health-interval=5s --health-timeout=5s --health-retries=10 \
  pgvector/pgvector:pg17 postgres -c max_connections=400
```

`max_connections=400` (WL-465): a single `make test` run against this
container peaks around 120 concurrent connections (measured directly on
hel01 — `SELECT count(*) FROM pg_stat_activity` polled through a full
`make test`, TEST_POSTGRES_DSN pointed at :15432). The default 100, and
even the 200 this instance had drifted to via an undocumented `ALTER
SYSTEM` on the running container, leave no room for two `test / test`
jobs landing within seconds of each other, which the shared-runner setup
routinely produces — hence "sorry, too many clients already"
(SQLSTATE 53300) failing unrelated tests on both jobs. 400 covers three
peak-concurrent runs with headroom; hel01 has 251GB RAM and no memory
limit on the container, so the extra connections cost nothing that
matters here.

**Applying this to the already-running container** needs a manual step —
recreating it loses the anonymous data volume, which is fine (CI-only,
each test creates/drops its own database, the migration template rebuilds
on first use), but it's still a live-infrastructure change nothing here
automates:

```
docker rm -f gha-ci-postgres
docker run -d --name gha-ci-postgres --restart=always \
  -p 127.0.0.1:15432:5432 \
  -e POSTGRES_PASSWORD=postgres \
  --health-cmd='pg_isready -U postgres' --health-interval=5s --health-timeout=5s --health-retries=10 \
  pgvector/pgvector:pg17 postgres -c max_connections=400
```

Or, to keep the same container and volume: `ALTER SYSTEM SET
max_connections = 400;` via `docker exec gha-ci-postgres psql -U postgres`,
then `docker restart gha-ci-postgres` (`max_connections` needs a restart,
not just a reload).

Port 15432, not 5432 — hel01 already runs the project's own local-dev
compose stack (`worklode-event-stream-postgres-1`) bound to
`127.0.0.1:5432`; this container is CI-only and does not share its
lifecycle. Each test creates and drops its own database, so sharing this
one instance across every self-hosted CI run is safe by the same
convention local dev already relies on (see root `CLAUDE.md`).

`_test.yml` takes the DSN as a `postgres-dsn` input rather than hardcoding
`localhost:5432`; `pr-checks.yml` supplies the right one per `trusted`. The
`e2e smoke test` step needs the same env var passed explicitly too — it has
no `postgres-dsn`-aware default of its own, only a hardcoded
`localhost:5432` fallback that happened to match the old `services:`
container. That gap shipped in the first self-hosted run of this workflow
and was caught immediately by CI going red; there is no other guard against
it recurring in a future step that reads `TEST_POSTGRES_DSN`.

The `gha-pgvector` runner label makes that dependency a scheduling
constraint, not just a convention `test` happens to rely on: requiring it
(rather than leaving Postgres reachability implicit, the way `lint` doesn't
need it at all) means a second self-hosted runner added later — without
this sidecar — can never be handed a `test` job it would immediately fail.
Label it when its own `gha-ci-postgres` exists and is reachable, not before:

```
gh api -X POST repos/sunstoneinstitute/worklode/actions/runners/<id>/labels -f "labels[]=gha-pgvector"
```

`test` also requires `gha-buildcache`, covered next — Postgres reachability
and cache-restore-skipping are two separate facts about hel01, and neither
implies the other.

## Persistent build caches (`gha-buildcache`)

`gha-buildcache` asserts one general fact about a runner: **its local disk
persists build-cache state across job runs**, so a job can skip the
`actions/cache` round trip entirely rather than restore-on-every-run /
save-only-on-main. Three jobs rely on it, for caches that live in two
different places:

- **`test` and `lint`** use Go's own `GOCACHE`/`GOMODCACHE`, which `go env`
  resolves under `$HOME` by default. Nothing needs preparing there; the
  caches just exist once anything has run `go build`/`go test` on the box.
  `_test.yml` and `_lint.yml` both skip their `Restore`/`Save Go cache` steps
  (`lint` has no `Save` step — only `main` writes the cache, and only `test`
  runs there) and the `Go cache paths` step whenever `gha-buildcache` isn't
  in `runs-on`.
- **`build-image`** relies on a **persistent BuildKit state volume**. On a
  `gha-buildcache` runner it passes `docker/setup-buildx-action` a fixed
  `name: worklode-ci` plus `keep-state: true`, which turns the job's teardown
  into `buildx rm --keep-state`: the builder record goes, the Docker volume
  behind it (`buildx_buildkit_worklode-ci0_state`) stays. The next run
  recreates that builder by name, re-attaches the volume, and finds both the
  layer cache and the Dockerfile's `RUN --mount=type=cache` contents
  (`/go/pkg/mod`, `/root/.cache/go-build`) already warm. Nothing needs
  exporting, so the job skips `buildkit-cache-dance` and passes empty
  `cache-from`/`cache-to` instead of `type=gha`. Hosted runners get an
  anonymous throwaway builder and both round trips, since their VM is gone
  after the job.

  Both hel01 runners share the one `worklode-ci` builder rather than getting
  one each: they already share the Docker daemon, and BuildKit serializes
  concurrent builds itself — the reason `$HOME` separation is still needed for
  Go's caches (see *Two executors* above) does not apply here.

```
gh api -X POST repos/sunstoneinstitute/worklode/actions/runners/<id>/labels -f "labels[]=gha-buildcache"
```

The volume grows unbounded. Trim it under disk pressure — this discards build
cache only, never images:

```
docker buildx prune --builder worklode-ci --filter until=168h
```

Not `driver: docker` (the host daemon's own builder), which would need no
volume at all: pushing to a registry from that driver requires dockerd's
containerd image store, which hel01 does not have enabled.

`build-image` used to persist its cache mounts to a prepared
`$HOME/.cache/gha-buildcache/{mod,build}` directory via
`buildkit-cache-dance`. That directory is now unused on both runners and can
be deleted. Its rule still binds anything that replaces it: **a self-hosted
cache path must be absolute and outside the checkout**, not workspace-relative
the way `ubuntu-latest`'s `go-cache-mount/` is. `actions/checkout`'s clean
step (`git clean -ffdx`) wipes anything untracked inside the checkout every
run. This isn't theoretical: the first self-hosted `build-image` run (before
`gha-buildcache` existed) wrote its cache mount to the relative path, and
`buildkit-cache-dance`'s extraction ran as root inside a container, leaving
root-owned files under `go-cache-mount/` in the shared checkout. The *next*
job on the runner — of any kind, since every job shares the one checkout at
`_work/worklode/worklode` — failed at `actions/checkout` itself, unable to
`unlink` those files as the unprivileged `ghrunner`. Recovery needed
`sudo rm -rf` from outside the job entirely; nothing inside a job can clean up
a mess a less-privileged user can't delete.

`_test.yml`, `_lint.yml` and `_build-image.yml`'s `runs-on` inputs are all
JSON arrays (`fromJSON(inputs.runs-on)`) for this reason — `test` and
`build-image` require more than one label at once (`gha-pgvector` +
`gha-buildcache`, `docker` + `gha-buildcache`), and `lint` requires
`self-hosted` + `gha-buildcache` explicitly rather than treating a persistent
cache as implied by the bare `self-hosted` label.

## Docker for `build-image`

`docker/setup-buildx-action` and `docker/build-push-action` need a reachable
Docker daemon — there is no daemonless mode here, unlike the Postgres case
above. That's the one job that actually requires the isolation trade-off in
the *Isolation* section: `ghrunner` is in the `docker` group, and
`build-image`'s `runs-on` targets the `docker` label rather than the bare
`self-hosted` one, for the same "scheduling constraint, not convention"
reason `gha-pgvector` exists — a future self-hosted runner without Docker
access must never be handed this job.

Sanity-checked with a real `docker buildx build` as `ghrunner` against this
branch before wiring `pr-checks.yml` to it, matching what
`docker/build-push-action` does under the hood.

```
gh api -X POST repos/sunstoneinstitute/worklode/actions/runners/<id>/labels -f "labels[]=docker"
```

`docker` and `gha-buildcache` are independent facts — a future runner could
have Docker access set up before its build cache is worth reusing, or vice
versa — so `build-image`'s `runs-on` requires both rather than treating either
as implied by the other: `runs-on: ["docker","gha-buildcache"]`. Together they
are what let the job keep one builder across runs and drop every cache export;
see *Persistent build caches* above.

## `/tmp` inode exhaustion (WL-188)

`/tmp` on hel01 is a tmpfs with a fixed `nr_inodes=1048576` — `df -h /tmp`
can report terabytes free while `df -i /tmp` is pinned at 100%, and the
failure that follows is `ENOSPC` on whichever job happens to write next, with
nothing in the error pointing at inodes. This bit once already (WL-147/WL-188):
a test harness bug downloaded the Go module cache into a per-run temp `HOME`
and failed to clean it up, and forty-six abandoned trees at ~18,000 inodes
each exhausted the tmpfs while it still showed 100+ GB free.

**Diagnose it fast**: `df -i /tmp` next to `df -h /tmp` — a huge gap between
`Use%` on the two is this failure, not a real disk-space problem. `du --inodes
/tmp/*` finds the offender.

**Fixed at the runner level**: both `hel01` and `hel01-2` set `TMPDIR` in
their systemd units, pointing at a directory on `/dev/md2` (real inodes, 2.9
TB free) instead of the tmpfs — `/home/ghrunner/tmp` for `hel01`,
`/home/ghrunner/runner2-home/tmp` for `hel01-2` (matching each runner's own
cache-separation directory, see *Two executors* above). Any job or test
harness that respects `TMPDIR` (Go's `os.TempDir()`, most `mktemp` usage)
no longer touches the tmpfs at all, regardless of which repo or job leaks.
This is scoped to the two runner services only — local dev, `docker compose`,
and interactive shells on hel01 still share the tmpfs `/tmp`, so a bug outside
CI can still refill it; watch for it with the `df -i` check above rather than
assuming the tmpfs is now safe from every source.

Considered and not done: raising or dropping tmpfs `nr_inodes` (treats the
symptom, not the source, and a wrong value in either direction just moves the
threshold); a systemd-tmpfiles aging rule on `/tmp` (host-wide blast radius
for a runner-specific problem — worth revisiting if a non-runner source turns
out to be the one refilling it).

## The agent host (`gha-agent-host`)

hel01 also hosts the long-lived Claude Code worker sessions. They are not a CI
job: `agent-host.yml` is a **babysitter** that reconciles them every 15 minutes
— create what is missing, respawn what died, touch nothing healthy — and
`agent-host-rules.yml` manages the permission rules they run under. Neither is
ever reachable from `pull_request`; both are `schedule`/`workflow_dispatch`
only, so the *Trust boundary* above is untouched.

`gha-agent-host` asserts what those jobs need and nothing else: **tmux and
`claude` are installed, and the persistent `/home/ghrunner/gha-agent` tree
exists**. Not the bare `self-hosted` label (a future runner without any of that
must never be handed this job) and not `hel01` (a host identity, not a
capability). `agent-host.yml` also requires `gha-buildcache`, because it builds
the `lode` binaries and skips the `actions/cache` round trip like every other
job here.

```
gh api -X POST repos/sunstoneinstitute/worklode/actions/runners/<id>/labels -f "labels[]=gha-agent-host"
```

### Layout

Everything durable lives at an absolute path outside the checkout, for two
reasons this document has already established. `actions/checkout`'s clean step
erases anything untracked inside `_work/worklode/worklode`, and **`$HOME` is
not stable**: `hel01-2` sets its own `HOME` (see *Two executors*), so a job that
resolved `~/.config/worklode` would read a different file depending on which
runner picked it up. The agent's `HOME` is therefore named explicitly, never
inherited.

```
/home/ghrunner/gha-agent/
  bin/          lode, lode-hook, lode-statusline + the agent-host scripts
  home/         the supervisors' HOME
    .config/worklode/config.toml     server = https://worklode.dev.sunstoneinstitute.ai
    .config/worklode/token           0600, "<server> <token>" — one line per server
    .claude/settings.json            the autoMode rules, user scope
  repo/         the primary clone: owns .git, stays on main, never worked in
  agents/<id>/  one linked worktree per worker, on branch agent-main/<id>;
                its task worktrees land in its own .worktrees/
  env           0600, sourced by each window's shell
  logs/         pane captures, 0600, pruned after 7 days
```

### One working tree per worker, one `.git` for all of them

Workers share the object store and never a working tree. Sharing `.git` is
what makes a second worker cheap: one fetch, one LFS cache, one 8 MB object
store. Sharing a *working tree* would be a bug — it is one HEAD, one index and
one set of untracked files, so several agents in `repo/` would contend over
the branch the babysitter fast-forwards, over build output, and over each
other's `git` invocations.

So `repo/` is the primary clone and nobody works in it: it owns `.git`, it is
the checkout `worktree.MainRoot` resolves to (which is where the repo-wide half
of `lode install` lands, once, however many workers run), and it is where the
single `git fetch` per tick happens. Each worker gets a linked worktree at
`agents/<id>` on its own branch.

**The branch is `agent-main/<id>`, and it cannot be `main/<id>`.** Git stores
`refs/heads/main` as a file, so `refs/heads/main/<id>` would need `main` to be
a directory — a directory/file conflict git refuses:

```
fatal: cannot lock ref 'refs/heads/main/gha-agent1': 'refs/heads/main' exists
```

(The `claude/<name>` branches this repo already uses work only because no plain
`claude` branch exists.)

These branches are **local-only and must never be pushed**. They never receive
commits either — agents commit on task branches inside their own
`.worktrees/` — so `reconcile.sh` tracks `origin/main` with a hard reset rather
than a merge: there is nothing to preserve and nothing to conflict. A worker
tree with local changes is left alone and reported, so one agent's mess cannot
stall the others.

`lode install` therefore runs **per worker tree**, not once in the primary. The
propagation that populates each task worktree's `settings.local.json` is gated
on *its own root* having been installed, and every agent's root is now its own
worktree. `internal/worktree`'s
`TestTaskIDIsFalseForAWorktreeOutsideTheBase` pins the CLI behaviour this rests
on: `lode worktree next`'s "already inside a worktree" guard is a question about the
path — exactly one segment below `.worktrees` — not about being the main
checkout, so it runs happily in `agents/<id>`.

The tmux server has its own socket, `tmux -L gha-agent`, so it never collides
with an interactive tmux. The socket is keyed by uid rather than `$HOME`, so
both runners reach the same server.

```
tmux -L gha-agent list-windows -t agents
tmux -L gha-agent attach -t agents:gha-agent1
```

Three repo secrets are required: `LODE_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, and
`WORKER_GITHUB_TOKEN` (a fine-grained PAT scoped to this repo, contents + PR
write). The job's own `GITHUB_TOKEN` expires when the job ends and is useless
to a session that outlives it.

### Adding a worker

`.github/agent-host/workers.json` is the fleet. Each entry is a window name and
one filter string:

```json
{ "window": "gha-chore1", "filter": "--project worklode --kind chore" }
```

That string is used twice — `supervisor.sh` substitutes it for `$ARGUMENTS` in
the `start-agent-loop` skill body, and `poke.sh` passes it to `lode worker
listen` — so the sidecar wakes on exactly the work its supervisor could claim.
Only flags the skill accepts belong in it: `--project`, `--kind`,
`--strict-focus`.

Each worker gets two windows — `<name>` running the Claude session and
`<name>-poke` running the sidecar that nudges it when work appears — plus its
own worktree at `agents/<name>`, all created by `reconcile.sh`. Retiring a
worker means removing its entry here; `git worktree prune` only clears
registrations whose directories are already gone, so deleting the tree itself
stays a deliberate manual step and can never take an in-flight task worktree
with it. Health is
judged by pane state — dead, or fallen back to a bare shell, means respawn.
That is a denylist on purpose: `claude` runs as `node` today, and matching only
`node` would turn any change in how it is launched into an endless respawn loop
against a session that was working fine.

### Rules are committed, not accumulated

`ghrunner` is in the `docker` group, which the *Isolation* section above calls
root-equivalent on this host. An unattended agent here is effectively
unsandboxed, so what it may do is reviewed in a diff rather than left to
accumulate on the box: `.github/agent-host/settings.json` holds the `autoMode`
block, and the babysitter installs it to the agent `HOME` on every tick.

It has to be **user scope**. The auto-mode classifier deliberately ignores a
repository's own `.claude/settings.json` so that a hostile repo cannot grant
itself permissions; rules committed there would silently do nothing.

`/auto-mode-setup` is interactive — it drafts a block and asks a human to
accept it — so `agent-host-rules.yml` splits the job: `action=setup` opens a
`gha-rules` window to run it in, `action=capture` promotes the result into a
pull request.

### Scrollback stays on the box

This repository is **public**, so Actions logs and job summaries are
world-readable. Pane captures therefore go to `logs/` at 0600 and the step
summary carries only window state and what was done. The `dump_scrollback`
dispatch input prints a tail into the job log for debugging; it defaults to
false, and it publishes whatever the agent had on screen.

### Teardown

```
tmux -L gha-agent kill-server
```

Disable the `Agent host` workflow first, or the next tick rebuilds the fleet.
That same tick is the recovery path for the one failure mode tmux's own
daemonizing does not cover: the runner unit's default `KillMode=control-group`
takes down everything in the service cgroup when the runner service restarts,
the tmux server included.

## Extending self-hosted coverage

`_test.yml`, `_lint.yml` and `_build-image.yml` all take a `runs-on` input
(default `ubuntu-latest` / `["ubuntu-latest"]`) — any reusable workflow gains
hel01 the same way, by threading that input through from its caller's
`gate.trusted` output. Pick the label for what the job actually needs, not
for convenience: `gha-pgvector` for Postgres, `docker` for the Docker
socket, `gha-buildcache` for skipping a cache round trip, the bare
`self-hosted` for none of the above. If a job needs
more than one, require all of them — don't let one imply another that
happens to be true today. Don't require a label a job doesn't need, and
don't let a job that does need one fall back to the bare `self-hosted` —
any of these breaks the point of tagging by requirement at all. Do **not**
add `services:` to a job that might run self-hosted: GitHub starts service
containers unconditionally once a job declares them, which would need
`ghrunner` to reach the Docker socket outside of the `docker` label's own
job. Gate container provisioning with a step-level `if:` instead, as
`_test.yml` does. And do not assume a relative, workspace-local directory
persists on self-hosted just because the runner itself does —
`actions/checkout`'s clean step erases it every run and, as the incident
above shows, can fail outright if something wrote to it as root; only a
path outside the checkout, like `gha-buildcache`'s, actually survives.
