# Self-hosted CI runner (hel01)

`test`, `lint` and `build-image` in `pr-checks.yml` run on a self-hosted
GitHub Actions runner on `hel01` when the triggering PR is trustworthy;
`validate-kustomize`, `obsidian`, and every PR the gate doesn't trust stay on
`ubuntu-latest`. The payoff is a warm, persistent `GOCACHE`/`GOMODCACHE` — no
`actions/cache` round trip for Go, no re-fetching `go.sum` on every run —
plus 48 cores and a persistent Docker build cache instead of a hosted
runner's 4 cores and a from-scratch layer cache each time.

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

Registered as a repo-level runner (not org-level) with labels `self-hosted,
Linux, X64, hel01, gha-pgvector, docker, gha-buildcache`. `lint` targets the
bare `self-hosted` label; `test` targets `gha-pgvector` and `gha-buildcache`;
`build-image` targets `docker` and `gha-buildcache` — see the sections below
for why each label exists rather than every job sharing `self-hosted`.

Installed as a systemd service:

```
systemctl status actions.runner.sunstoneinstitute-worklode.hel01.service
```

Reinstall/reconfigure from `/home/ghrunner/actions-runner` (`config.sh`,
`svc.sh`) per GitHub's own self-hosted runner docs; a fresh registration
token comes from `gh api -X POST
repos/sunstoneinstitute/worklode/actions/runners/registration-token`. A
group-membership change (like the docker group above) needs the service
restarted — `usermod` alone doesn't affect an already-running process:

```
sudo systemctl restart actions.runner.sunstoneinstitute-worklode.hel01.service
```

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
  pgvector/pgvector:pg17
```

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
(rather than the bare `self-hosted` `lint` uses) means a second self-hosted
runner added later — without this sidecar — can never be handed a `test`
job it would immediately fail. Label it when its own `gha-ci-postgres`
exists and is reachable, not before:

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
save-only-on-main. Two jobs rely on it, for caches that live in different
places:

- **`test`** (and `lint`, informally — see the note below) uses Go's own
  `GOCACHE`/`GOMODCACHE`, which `go env` resolves under `$HOME` by default.
  Nothing needs preparing there; the caches just exist once anything has run
  `go build`/`go test` on the box. `_test.yml` skips its `Restore`/`Save Go
  cache` steps and the `Go cache paths` step whenever `gha-buildcache` isn't
  in `runs-on`.
- **`build-image`** needs a specific *prepared* directory instead, because
  it doesn't get Go's defaults for free: `buildkit-cache-dance` extracts the
  Dockerfile's `RUN --mount=type=cache` contents (`/go/pkg/mod`,
  `/root/.cache/go-build`) into a host directory it's told about, and that
  directory has to exist and be writable.

```
sudo -u ghrunner mkdir -p /home/ghrunner/.cache/gha-buildcache/{mod,build}
gh api -X POST repos/sunstoneinstitute/worklode/actions/runners/<id>/labels -f "labels[]=gha-buildcache"
```

**It has to be an absolute path outside the checkout**, not a directory
relative to the workspace the way `ubuntu-latest` uses `go-cache-mount/`
today. `actions/checkout`'s clean step (`git clean -ffdx`) wipes anything
untracked inside the checkout on every run — a relative cache-mount
directory would never survive to the next job. This isn't theoretical: the
first self-hosted `build-image` run (before `gha-buildcache` existed) wrote
its cache mount to the old relative path, and `buildkit-cache-dance`'s
extraction step ran as root inside a container, leaving root-owned files
under `go-cache-mount/` in the shared checkout. The *next* job on the
runner — of any kind, since every job shares the one checkout at
`_work/worklode/worklode` — failed at `actions/checkout` itself, unable to
`unlink` those files as the unprivileged `ghrunner`. Recovery needed
`sudo rm -rf` on the poisoned directory from outside the job entirely; nothing
inside a job can clean up a mess a less-privileged user can't delete. A
`gha-buildcache`-labeled directory can't have this failure mode, because
nothing ever asks `actions/checkout` to clean it.

`_test.yml` and `_build-image.yml`'s `runs-on` inputs are both JSON arrays
(`fromJSON(inputs.runs-on)`) for this reason — each now requires more than
one label at once (`gha-pgvector` + `gha-buildcache`, `docker` +
`gha-buildcache`) — unlike `_lint.yml`'s single-label string.

**Known asymmetry:** `lint` still skips its own Go-cache restore/save purely
because `runs-on` isn't `ubuntu-latest`, the same implicit assumption this
label exists to replace for `test`. It hasn't been converted to require
`gha-buildcache` explicitly — do that the next time `lint` changes, using
`test`'s conversion as the template.

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
have Docker access set up before its cache directory exists, or vice versa —
so `build-image`'s `runs-on` requires both rather than treating either as
implied by the other: `runs-on: ["docker","gha-buildcache"]`.

## Extending self-hosted coverage

`_test.yml`, `_lint.yml` and `_build-image.yml` all take a `runs-on` input
(default `ubuntu-latest` / `["ubuntu-latest"]`) — any reusable workflow gains
hel01 the same way, by threading that input through from its caller's
`gate.trusted` output. Pick the label for what the job actually needs, not
for convenience: `gha-pgvector` for Postgres, `docker` for the Docker
socket, `gha-buildcache` for skipping a cache round trip, the bare
`self-hosted` for none of the above (as `lint` uses today). If a job needs
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
