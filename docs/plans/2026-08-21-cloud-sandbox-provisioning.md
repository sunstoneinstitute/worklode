---
status: draft
covers:
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-0
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-1
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-2
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-2.1
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-2.2
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-3
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-3.1
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-3.2
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-4.1
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-4.2
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-4.3
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-4.4
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-5
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-6
    coverage: full
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-7
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-8
    coverage: none
  - spec: docs/specs/038-worklode-in-a-cloud-sandbox.md#sec-9
    coverage: full
---
# Cloud sandbox provisioning (spec 038) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the worker environment reproducible from the repository, per
spec 038: one repo-committed bootstrap (`scripts/bootstrap.sh`) that is the
only description of the environment; a worker image family (`docker/base`,
`docker/generic`) that caches it, with agent hooks installed at image build
time; content-addressed image tags; and the e2e proof that a session holding
only `LODE_SERVER` and `LODE_TOKEN` — no config file, no keychain — claims,
commits and closes a task.

**Why one plan:** every section is implementable against today's tree.
`lode install --agent all` and `--no-statusline` exist
(`internal/cmd/install.go`); the three tool-version sources §2 names exist
(`go.mod` + its `tool github.com/a-h/templ/cmd/templ` directive,
`scripts/tailwind.sha256` read by `scripts/fetch-tailwind.sh`); CI already
reads `go-version-file: go.mod`; e2e already drives the real CLI in-process
(`runLodeCLI`, `e2e/next_test.go`). Nothing here waits on 017 or 032 — the
spec's §4.4 and §7 fence both off.

**Coverage notes.** `none` rows are standing rules and declared non-work,
not gaps: §§0–1 orient; §2.1/§2.2 are declined-with-trigger decisions that
this plan must not un-decline (no tool manager, no cross-platform package
schema); §4.1 is the entry-point contract the `lode-worker` agent already
follows (task 7 exercises it but builds nothing for it); §4.4 belongs to
spec 017; §5 constrains task shape (build the human path only — dispatch
substitutes values, never code); §7 lists exclusions; §8's open questions
stay open — task 6 deliberately builds images without publishing them
(§8 Q1, registry unsettled) and files the follow-up. §4 itself has no body
(all content is §§4.1–4.4) and is not claimed separately.

**Plan-level decisions** (within spec bounds, so implementers do not
re-decide them):

- **Bootstrap does not install Go.** It verifies the installed Go satisfies
  `go.mod` and otherwise fails loudly naming what is missing — §2.2's rule.
  Installing Go is the platform layer's job: `actions/setup-go` with
  `go-version-file: go.mod` in CI, the Dockerfile in images, the human on a
  laptop. Anything downstream of Go (module download, `go tool templ`,
  Tailwind via `scripts/fetch-tailwind.sh`) is bootstrap's job.
- **The tag is computed by `scripts/worker-image-tag.sh`**: sha256 over a
  canonical concatenation of exactly the §3.2 inputs — the base image
  digest, `.worklode/Dockerfile` (empty contribution when absent), and the
  pin files the bootstrap reads (`go.mod`, `scripts/tailwind.sha256`) —
  truncated to 12 hex chars. Pure computation, no Docker daemon needed.
- **Worker images run Debian stable-slim** as a non-root `worker` user with
  a writable `HOME`, because `lode install --agent all` writes hook
  configuration into agent settings files under `HOME` at build time, and
  §3.1 requires shell + git + CA certificates. The root `Dockerfile`
  (server, distroless) is untouched.

## Global constraints

- The image is a cache of `scripts/bootstrap.sh`, never a second source of
  truth (§3): any tool step added to a Dockerfile must be bootstrap calling
  or the Dockerfile invoking bootstrap — no version literal may appear in a
  Dockerfile or workflow that `go.mod` or `scripts/tailwind.sha256` already
  carries.
- Hook install at image build time is exactly
  `lode install --agent all --no-statusline` (§4.2).
- No image is pushed to any registry in this plan — §8 Q1 is unsettled.
  Build, test, discard.
- No server, store, or API change anywhere in this plan, so no new
  `worklode_*` metrics are owed.
- `e2e/` drives public surfaces only — HTTP API and the real CLI entry
  point, never direct store writes.
- Every task leaves `make test` and `make vet` green; shell scripts pass
  `shellcheck`.

## Tasks

### Task 1 — scripts/bootstrap.sh, the one description of the environment

```yaml
kind: feature
priority: medium
blockedBy: [ ]
```

Create `scripts/bootstrap.sh` (bash, `set -euo pipefail`), taking a bare
checkout to a state where `go generate ./...` and `go test -trimpath ./...`
both succeed (§9.1). It delegates to the existing single sources rather than
restating any version (§2): verify `go` is present and satisfies `go.mod`
(compare `go env GOVERSION` against the `go` directive; on failure, exit
non-zero naming the missing/mismatched tool — §2.2's fail-loudly rule);
`go mod download`; ensure templ is usable via `go tool templ version` (the
`tool` directive in `go.mod` — nothing to install); fetch Tailwind via the
existing `scripts/fetch-tailwind.sh`. Idempotent: a second run is a fast
no-op. Print each step and end with a one-line summary.

- [ ] Write the script; `shellcheck scripts/bootstrap.sh` clean.
- [ ] Prove it locally: run it in a fresh clone (or after
      `git clean -xdf` in a scratch copy — not in this worktree), then
      `go generate ./...` and `make test` pass.
- [ ] Document it in `CLAUDE.md`'s Commands section (one line) — it is the
      bootstrap verb §2 promises.
- [ ] Commit.

### Task 2 — CI consumes bootstrap.sh instead of restating tool setup

```yaml
kind: chore
priority: medium
skills:
  - worklode-ci
blockedBy: [1]
```

Make CI call `scripts/bootstrap.sh` rather than restating any tool step
(§9.2). In `.github/workflows/_lint.yml`, replace the inline
`./scripts/fetch-tailwind.sh` step (and any other tool-fetch step that
bootstrap now owns) with one `./scripts/bootstrap.sh` step after
`actions/setup-go`; sweep `_test.yml` and the other workflows for restated
tool steps the same way. `actions/setup-go` with `go-version-file: go.mod`
stays — it reads the single source, it does not restate a version, and
installing Go is the platform layer's job (plan-level decision above).

- [ ] Edit the workflows; no version literal remains that `go.mod` or
      `scripts/tailwind.sha256` already carries.
- [ ] Green CI run on the PR branch proves the substitution.
- [ ] Commit.

### Task 3 — docker/base: the minimum that can claim a task

```yaml
kind: feature
priority: medium
blockedBy: [ ]
```

Create `docker/base/Dockerfile` (§3.1): multi-stage — a `golang` builder
stage compiling `lode` with `-trimpath` (mirror the root `Dockerfile`'s
build flags), then a `debian:stable-slim` runtime with shell, `git`,
`ca-certificates`, and `lode` on `PATH`. Non-root `worker` user with a
writable `HOME`. At build time run
`lode install --agent all --no-statusline` (§4.2) so the agent lifecycle
hooks pre-exist any session; no status line, no
`claude-ctx-<session>.json` bridge file. The root `Dockerfile` (server
image) is not touched.

- [ ] Write the Dockerfile; `docker build -f docker/base/Dockerfile .`
      succeeds locally.
- [ ] Smoke-assert in the built image:

```bash
docker run --rm <img> lode --version
docker run --rm <img> sh -c 'ls "$HOME"/.claude/settings.json'  # hooks present
```

- [ ] Assert no statusline artifact is configured in the written settings.
- [ ] Commit.

### Task 4 — docker/generic: base plus the bootstrap toolchains

```yaml
kind: feature
priority: medium
blockedBy: [1, 3]
```

Create `docker/generic/Dockerfile` (§3.1): `FROM` the base image, add the
toolchains `scripts/bootstrap.sh` needs — a Go toolchain satisfying
`go.mod`'s directive (installed by the Dockerfile: platform layer) — and
run `scripts/bootstrap.sh` against a copy of the repo's pin files so the
tool downloads (Tailwind, module cache warm-up as far as sensible) are
baked in. Every tool version must reach the image through bootstrap or the
pin files it reads — a literal in the Dockerfile that `go.mod` or
`scripts/tailwind.sha256` already carries is the drift §2 exists to
prevent, and a build-arg default counts as a literal.

- [ ] Write the Dockerfile; local build succeeds.
- [ ] Smoke-assert: `docker run --rm <img> go version` satisfies `go.mod`;
      the Tailwind binary fetched by bootstrap is present and executable.
- [ ] Commit.

### Task 5 — Content-addressed worker image tags

```yaml
kind: feature
priority: medium
blockedBy: [3, 4]
```

Create `scripts/worker-image-tag.sh` computing the §3.2 tag: sha256 over a
canonical, delimited concatenation of the base image digest (argument),
`.worklode/Dockerfile` when present (empty contribution when absent), and
the pin files the bootstrap reads — `go.mod` and `scripts/tailwind.sha256`
— truncated to 12 hex chars, printed to stdout. No Docker daemon needed:
inputs are files plus one digest string, so the property tests are pure.
Add `scripts/worker_image_tag_test.py` (pytest, matching the existing
`scripts/*_test.py` pattern) covering §6's third bullet / §9.5:

```python
def test_same_inputs_same_tag(tmp_path): ...      # run twice, tags equal
def test_each_input_changes_tag(tmp_path): ...    # flip each input, tag differs
def test_missing_project_dockerfile_ok(tmp_path): ...  # absent != empty-string collision
```

- [ ] Script + tests; `shellcheck` clean, pytest green.
- [ ] Commit.

### Task 6 — CI job: images build, and image and script agree

```yaml
kind: chore
priority: medium
skills:
  - worklode-ci
blockedBy: [2, 3, 4, 5]
```

Add a CI job (new `_images.yml` reusable workflow wired into
`pr-checks.yml`, path-filtered to `docker/**`, `scripts/bootstrap.sh`,
`scripts/worker-image-tag.sh`, `go.mod`, `scripts/tailwind.sha256`) that:
builds `docker/base` and `docker/generic`; runs `scripts/bootstrap.sh`
inside each over a fresh checkout — divergence between image and script is
the failure that matters and the one the unit suite cannot see (§6);
asserts hooks are present and no status line is configured (§9.3); and
computes the tag via `scripts/worker-image-tag.sh` for the job log. Build,
test, discard: no push — where images live and who may pull them is §8's
open question 1.

- [ ] Workflow added and wired; green on the PR branch.
- [ ] Append the registry/publishing gap to `docs/follow-ups.md`
      (per the filing-follow-ups skill), naming spec 038 §8 Q1.
- [ ] Commit.

### Task 7 — e2e: environment-only authentication

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Add `e2e/sandbox_env_test.go` (build tag `e2e`), following
`e2e/next_test.go`'s `runLodeCLI` pattern: a session whose **only**
credentials are `LODE_SERVER` and `LODE_TOKEN` — point `HOME` (and
`XDG_CONFIG_HOME`) at an empty `t.TempDir()` so no config file or keychain
can be consulted — claims a task with `lode next --json`, gets a worktree
under `.worktrees/` with the lease bound to it exactly as on a laptop,
commits in it, and closes the task through the public API (§4.3, §6 first
bullet, §9.4). Assert the lease binding via public surfaces (`lode status`
/ task JSON), never a direct store read.

- [ ] Test written and red-then-green against the in-process server.
- [ ] `go test -trimpath -tags e2e ./e2e/ -run TestSandboxEnv` green with
      `TEST_POSTGRES_DSN` reachable.
- [ ] Commit.
