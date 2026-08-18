# Self-hosted CI runner (hel01)

`test` and `lint` in `pr-checks.yml` run on a self-hosted GitHub Actions
runner on `hel01` when the triggering PR is trustworthy; every other job
(`build-image`, `validate-kustomize`, `obsidian`) and every PR the gate
doesn't trust stay on `ubuntu-latest`. The payoff is a warm, persistent
`GOCACHE`/`GOMODCACHE` — no `actions/cache` round trip, no re-fetching
`go.sum` on every run — plus 48 cores instead of a hosted runner's 4.

## Trust boundary

`worklode` is a **public** repo, and a self-hosted runner executes arbitrary
job code with the runner user's access to the host. `pr-checks.yml`'s `gate`
job computes a `trusted` output — true only when the PR's head repo is this
repo (`github.event.pull_request.head.repo.full_name ==
github.repository`), independent of `run` (which the `can-be-tested` label
or author association can also set true). **A fork PR never gets `trusted`,
regardless of label or association** — it always runs on `ubuntu-latest`.
`lint`/`test` route their `runs-on` input off `trusted`, not `run`.

Do not widen `trusted` to include forks from collaborators/members: hosted
CI already accepts that risk for a throwaway VM per job; hel01 is not
throwaway, and it holds this machine's SSH keys, kubeconfig, and other
credentials outside the runner user's reach only because of the isolation
below.

## Isolation

The runner runs as a dedicated **system user, `ghrunner`** — no `sudo`, no
`docker` group, no login shell used interactively, home `/home/ghrunner`
mode `0700`. It cannot read `~stig`, cannot reach the Docker socket, and
cannot escalate. Registered as a repo-level runner (not org-level) with
labels `self-hosted, Linux, X64, hel01, gha-pgvector`. `lint` targets it via
the bare `self-hosted` label; `test` targets `gha-pgvector` specifically —
see "Postgres for `test`" below for why that label exists rather than
`test` also using `self-hosted`.

Installed as a systemd service:

```
systemctl status actions.runner.sunstoneinstitute-worklode.hel01.service
```

Reinstall/reconfigure from `/home/ghrunner/actions-runner` (`config.sh`,
`svc.sh`) per GitHub's own self-hosted runner docs; a fresh registration
token comes from `gh api -X POST
repos/sunstoneinstitute/worklode/actions/runners/registration-token`.

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
`localhost:5432`; `pr-checks.yml` supplies the right one per `trusted`.

The `gha-pgvector` runner label makes that dependency a scheduling
constraint, not just a convention `test` happens to rely on: `runs-on:
gha-pgvector` (rather than the bare `self-hosted` `lint` uses) means a
second self-hosted runner added later — without this sidecar — can never
be handed a `test` job it would immediately fail. Label it when its own
`gha-ci-postgres` exists and is reachable, not before:

```
gh api -X POST repos/sunstoneinstitute/worklode/actions/runners/<id>/labels -f "labels[]=gha-pgvector"
```

## Extending self-hosted coverage

`_test.yml` and `_lint.yml` both take a `runs-on` input (default
`ubuntu-latest`) — any reusable workflow gains hel01 the same way, by
threading that input through from its caller's `gate.trusted` output. Use
`gha-pgvector` instead of `self-hosted` for a job that needs the Postgres
sidecar (as `test` does); use the bare `self-hosted` label for one that
doesn't (as `lint` does) — don't require `gha-pgvector` for a job that has
no actual Postgres dependency, and don't let a job that does need it fall
back to `self-hosted` alone. Do **not** add `services:` to a job that might
run self-hosted: GitHub starts service containers unconditionally once a
job declares them, which would require `ghrunner` to hold Docker access and
undo the isolation above. Gate container provisioning with a step-level
`if:` instead, as `_test.yml` does.
