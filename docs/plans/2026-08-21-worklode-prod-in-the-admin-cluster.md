---
status: draft
covers:
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-0
    coverage: none
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-1
    coverage: none
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-2
    coverage: full
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-3
    coverage: full
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-4
    coverage: full
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-4.1
    coverage: full
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-5
    coverage: none
  - spec: docs/specs/039-worklode-prod-in-the-admin-cluster.md#sec-6
    coverage: none
---
# Worklode prod in the admin cluster — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give worklode prod the placement spec 039 assigns it: a
`deploy/overlays/admin/` overlay serving `worklode.sunstoneinstitute.ai` from
the admin cluster, the promotion pipeline pointing at that overlay, the
spec's standing invariants enforced in CI, and the cutover from today's
placeholder `hzprod` overlay executed and then retired.

**What already exists (verified against the tree, 2026-08-21):**

- **§3 is implemented.** `LODE_INSTANCE_ENV` is parsed and validated in
  `internal/api/instanceenv.go` (`dev`/`prod`, anything else fails startup —
  `TestRunServeRejectsBadInstanceEnv`, `internal/cmd/serve_test.go:201`),
  wired in `internal/cmd/serve.go:151`, defaulted to `"prod"` in
  `deploy/base/configmap.yaml:13`, patched to `"dev"` by
  `deploy/overlays/hzdev/kustomization.yaml:27` and by `docker-compose.yml:59`.
  This plan claims §3 `full` on the strength of that existing code; Task 2
  adds the overlay-side enforcement, and no Go change is needed.
- **`deploy/overlays/hzprod/` is a placeholder, not a live prod.**
  `promote-prod.yml` says so: "hzprod has no cluster yet, so nothing consumes
  `last-deploy/prod` until one is provisioned". The admin overlay replaces it;
  Task 4's preflight confirms what, if anything, is live before the cutover.
- **The web session gate is in place** (plan
  `2026-08-14-web-ui-requires-a-login-provider.md`): `LODE_WEB_OPEN` is the
  explicit opt-in, unset in every overlay. §4 makes its absence on reachable
  instances a standing rule — Task 2 turns that into a check.

**Read first:**

- `docs/specs/039-worklode-prod-in-the-admin-cluster.md` — the whole spec;
  it is short. §4 is the overlay contract this plan builds to.
- `deploy/overlays/hzdev/kustomization.yaml` and
  `deploy/overlays/hzprod/kustomization.yaml` — the two existing overlays;
  the admin overlay is their shape with §4's values.
- `.github/workflows/promote-prod.yml` and
  `.github/workflows/pr-checks.yml:170` (`validate-kustomize`) — the two
  workflows that touch overlays.

## Global Constraints

Exact values from spec 039 §3–§4 — quote these, do not improvise:

- Ingress host **`worklode.sunstoneinstitute.ai`**, in both the TLS hosts
  list and the rule host. `LODE_PUBLIC_URL` is
  **`https://worklode.sunstoneinstitute.ai`**.
- `LODE_INSTANCE_ENV` values are **`dev`, `prod` — nothing else**; the
  default is **`prod`**; the admin overlay therefore patches nothing and a
  comment says the base default stands. Never repurpose
  `LODE_CLUSTER_ENV_MAP` to say where worklode runs — it describes the
  clusters worklode observes and stays **identical in every overlay**.
- OIDC client **`worklode-prod`** against the issuer already in the hzprod
  overlay (`https://auth.sunstoneinstitute.ai/realms/sunstone`).
- `secretStoreRef` is a **`ClusterSecretStore`** named for the admin cluster
  (`onepassword-admin-worklode`, following the `onepassword-<cluster>-worklode`
  pattern of both existing overlays). 1Password item and property names are
  the same as hzdev's — the stores differ, the items do not.
- **`LODE_WEB_OPEN` is never set** in anything under `deploy/`.
- The namespace carries **`sunstone.institute/ghcr-pull: "true"`** in the
  admin overlay as in both existing ones.
- No Go code, no migrations, no new server behaviour: the metrics and
  migration constraints that worklode plans normally carry do not apply here.
  If a task finds it needs a server change, stop — that is a plan defect.
- Placement grants no outward privilege (§5): nothing in this plan touches
  the GitHub App's repository selection or permission ceiling.
- Commit messages describe the change, never the plan file, and carry no
  `Co-authored-by:` trailers.

## Tasks

### Task 1 — Create the admin overlay

```yaml
kind: chore
priority: high
blockedBy: [ ]
```

Create `deploy/overlays/admin/` with the same three files as the existing
overlays: `kustomization.yaml`, `externalsecret-worklode-secrets.yaml`,
`externalsecret-worklode-secrets-catalog.yaml`. Start from
`deploy/overlays/hzprod/` and apply exactly these deltas:

- Ingress patch: both `/spec/tls/0/hosts` and `/spec/rules/0/host` become
  `worklode.sunstoneinstitute.ai`.
- ConfigMap patch: `op: add` `/data/LODE_PUBLIC_URL` =
  `"https://worklode.sunstoneinstitute.ai"` (hzprod never set it; hzdev's
  overlay shows the shape).
- ConfigMap patch: **no** `LODE_INSTANCE_ENV` entry, with a comment where
  hzdev has its patch:

  ```yaml
  # LODE_INSTANCE_ENV is deliberately not patched: the base default is
  # "prod" (039 §3), and this is the prod instance.
  ```

- Both ExternalSecrets: `secretStoreRef.name` becomes
  `onepassword-admin-worklode` (kind stays `ClusterSecretStore`). Item and
  property names are untouched.
- Everything else is carried over verbatim: `LODE_CLUSTER_ENV_MAP` byte-equal
  to the other overlays, `LODE_SKILL_SOURCES`, OIDC issuer, client id
  `worklode-prod`, the `sunstone.institute/ghcr-pull: "true"` namespace
  label, `images:` with `newTag: latest` (the promotion workflow stamps real
  versions onto `last-deploy/prod`).

Verification is the build plus targeted asserts:

```
kubectl kustomize deploy/overlays/admin | grep -c worklode.sunstoneinstitute.ai
# → 2 (tls host + rule host)
kubectl kustomize deploy/overlays/admin | grep LODE_INSTANCE_ENV
# → LODE_INSTANCE_ENV: prod   (from base, unpatched)
kubectl kustomize deploy/overlays/admin | grep -c LODE_WEB_OPEN
# → 0
```

CI proof: `validate-kustomize` in `pr-checks.yml` builds every directory
under `deploy/overlays/`, so the new overlay is covered with no workflow
change.

- [ ] `deploy/overlays/admin/` with the three files and the deltas above
- [ ] `kubectl kustomize deploy/overlays/admin` builds; the three asserts hold
- [ ] Commit

### Task 2 — Enforce the overlay invariants in CI

```yaml
kind: chore
priority: medium
blockedBy: [1]
```

Spec 039 states three standing rules that a copy-pasted overlay would
silently break. Add `scripts/check-overlays.sh` asserting, over every
`deploy/overlays/*/`:

1. **`LODE_WEB_OPEN` appears nowhere under `deploy/`** (§4: "never set, on
   this overlay or any other reachable instance").
2. **`LODE_CLUSTER_ENV_MAP` is byte-identical in every overlay that patches
   it** (§3/§4: it describes the observed clusters, not the placement).
3. **The admin overlay does not patch `LODE_INSTANCE_ENV`** and the base
   value is `prod`; any overlay that does patch it uses `dev` or `prod` only
   (§3's value table).

Plain grep/awk over the YAML sources is enough — no kustomize build, no new
toolchain. Follow the exit-code and output conventions of
`scripts/check-migrations.sh`. Wire it as a step in the `validate-kustomize`
job in `.github/workflows/pr-checks.yml` (same job: it is the overlay gate,
and a separate job would re-checkout for a one-second script).

Prove it catches what it claims: run it once with a scratch overlay setting
`LODE_WEB_OPEN: "1"` and confirm a non-zero exit naming the file, then
delete the scratch overlay.

- [ ] `scripts/check-overlays.sh` with the three asserts
- [ ] Step added to `validate-kustomize` in `pr-checks.yml`
- [ ] Demonstrated failure on a violating scratch overlay, then green
- [ ] Commit

### Task 3 — Point the promotion pipeline and docs at admin

```yaml
kind: chore
priority: medium
blockedBy: [1]
```

`.github/workflows/promote-prod.yml` promotes the dev image with
`overlay-path: deploy/overlays/hzprod/kustomization.yaml`. Change it to
`deploy/overlays/admin/kustomization.yaml`, and rewrite the stale header
comment ("hzprod has no cluster yet…") to say what is now true: prod is the
admin cluster's overlay (039 §2), and `last-deploy/prod` is consumed by the
admin cluster's Flux once Task 5 wires it. Also update the inline comment on
the two overlay paths ("Overlay dirs are named after the cluster") — still
true, the cluster is now `admin`.

Sweep prose: `README.md`'s deployment section and any doc under `docs/`
(excluding `docs/specs/` and `docs/plans/`, which are records) that names
`hzprod` as the prod placement. Do not touch specs or old plans.

- [ ] `promote-prod.yml` overlay-path and comments updated
- [ ] README/docs sweep for `hzprod`-as-prod prose
- [ ] Commit

### Task 4 — Cutover preflight and runbook

```yaml
kind: chore
priority: high
blockedBy: [1]
```

Spec 039 §4.1 names what the cutover moves: the GitHub App webhook URL and
link callback (001 §9.1, §9.3), the Keycloak `worklode-prod` client's
redirect URIs (001 §4), and the Postgres in `deploy/base/postgres.yaml`,
which moves with the deployment. Write the ordered runbook as
`docs/prod-cutover.md` (flat under `docs/`, like `self-hosted-runner.md`;
it is transitional and Task 6 deletes it once spent).

The runbook opens with a **preflight** whose answers gate the rest —
`promote-prod.yml` records that no hzprod cluster consumed `last-deploy/prod`,
so several steps may be no-ops:

- [ ] Preflight: is any worklode prod instance live today, and on which
      host? What URL does the GitHub App webhook currently point at, and
      what redirect URIs does the Keycloak `worklode-prod` client hold?
      Does the admin cluster have a `ClusterSecretStore` named
      `onepassword-admin-worklode`, and does the admin cluster's Flux have
      (or need) a Kustomization watching this repo's `last-deploy/prod`
      branch at `deploy/overlays/admin`? Does DNS for
      `worklode.sunstoneinstitute.ai` exist?
- [ ] Runbook body, ordered: freeze window (if data exists) → provision the
      missing preflight items (secret store, Flux wiring, DNS — these live
      in the admin-cluster/provisioning repos, which this repo's GitHub App
      deliberately cannot reach, so they are named as human steps) →
      `pg_dump`/restore into the admin cluster's Postgres **only if** a live
      instance holds data → run `promote-prod.yml` → re-point the GitHub App
      webhook URL and link callback → update the Keycloak client redirect
      URIs via the GitOps realm config → verify (login via
      `worklode-prod`, a signed GitHub webhook delivery, `/metrics`
      scraped).
- [ ] Each step names who can perform it (repo, dashboard, or cluster
      access) so Task 5's executor knows when to block rather than push
      through.
- [ ] Commit

### Task 5 — Execute the cutover

```yaml
kind: chore
priority: high
blockedBy: [3, 4]
```

Run `docs/prod-cutover.md` top to bottom. This task is done when the prod
instance is serving at `https://worklode.sunstoneinstitute.ai` from the
admin cluster: login through `worklode-prod` works, a GitHub webhook
delivery is accepted (HMAC-verified, visible in the log), and whatever data
the preflight found live has been carried over.

Most steps need access outside this repo (admin cluster, GitHub App
settings, Keycloak realm config). An executor without that access **blocks
the task naming the missing step** — placement is a cluster-level act and
half-cutting-over leaves two instances claiming the same registrations.

- [ ] Runbook executed; each checkbox in `docs/prod-cutover.md` ticked
- [ ] Verification triple holds (login, webhook, metrics)

### Task 6 — Retire the hzprod overlay and the spent runbook

```yaml
kind: chore
priority: medium
blockedBy: [5]
```

With prod live in the admin cluster, `deploy/overlays/hzprod/` contradicts
039 §2's table (prod→admin, dev→hzdev) and keeps a copy-paste source whose
values are wrong. Delete the directory and `docs/prod-cutover.md` (§4.1 is
"true only until it has happened"; the spec keeps the record). Sweep for
remaining `hzprod` references outside `docs/specs/` and `docs/plans/` —
after Task 3 there should be none, so this is a check, not a rewrite.

- [ ] `deploy/overlays/hzprod/` deleted; `validate-kustomize` and
      `check-overlays.sh` green
- [ ] `docs/prod-cutover.md` deleted
- [ ] `grep -rn hzprod --exclude-dir=docs .` shows only historical records
- [ ] Commit
