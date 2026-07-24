# hzdev CI/Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure worklode's CI into the Sunstone trigger/component pattern, add a `deploy/` Kustomize tree, and wire it to deploy merges to `main` onto the `hzdev` Hetzner cluster (via `last-deploy/hzdev`), plus a dormant `Promote to hzprod` workflow (`last-deploy/hzprod`, no consumer yet since the `hzprod` cluster doesn't exist).

**Architecture:** Three repos change. (1) `sunstoneinstitute/actions` (`~/git/sunstone/actions`) gets two small, reusable fixes: `compute-version@v1` gains plain-text `VERSION` file support (worklode is Go, has no `pyproject.toml`/`package.json`), and `validate-kustomize@v1` gains flat-overlay support (it currently only globs per-component subdirectories, which would silently validate zero overlays for worklode's flat `deploy/overlays/hzdev/` layout). (2) `worklode` (this repo) gets `.github/workflows/{pr-checks,deploy-hzdev,promote-hzprod}.yml` trigger workflows calling `_lint.yml`/`_test.yml`/`_build-image.yml` components, replacing the single `ci.yml`, plus a `deploy/base` + `deploy/overlays/{hzdev,hzprod}` Kustomize tree and a `VERSION` file. Images publish to `ghcr.io/sunstoneinstitute/worklode` (no GCP/WIF — hzdev has no Workload Identity Federation, per the org's ghcr.io-for-Hetzner convention). (3) `provisioning` (`~/git/sunstone/provisioning`) gets the Flux `GitRepository`/`Kustomization` for hzdev plus the per-app 1Password `ClusterSecretStore`/Connect-token chain, mirroring `trusthere`.

**Tech Stack:** GitHub Actions (reusable `workflow_call` workflows + `sunstoneinstitute/actions@v1` composite actions), Kustomize, Flux CD, External Secrets Operator + 1Password Connect, ghcr.io.

---

## Repo map

| Repo | Local path | What changes |
|---|---|---|
| `sunstoneinstitute/actions` | `~/git/sunstone/actions` | `compute-version/action.yml`, `validate-kustomize/action.yml` |
| `worklode` | `~/git/sunstone/worklode` (this repo) | CI workflows, `deploy/`, `VERSION`, `.github/dependabot.yml` |
| `provisioning` | `~/git/sunstone/provisioning` | `clusters/hzdev/flux-system/*`, `clusters/hzdev/external-secrets-config/*` |

**Dependency note:** `promote-hzprod.yml` (Task 15) calls `promote-images@v1`, which internally calls `compute-version@v1` by the **floating** `@v1` tag (not a pinned SHA). It will not succeed with a `VERSION` file until the Task 1 PR is merged into `sunstoneinstitute/actions` and the `v1` tag has slid onto it. That's fine — promotion is `workflow_dispatch`, run on demand, not blocking anything else in this plan.

---

## Phase A — `sunstoneinstitute/actions`

### Task 1: `compute-version@v1` — support a plain-text `VERSION` file

**Files:**
- Modify: `~/git/sunstone/actions/compute-version/action.yml`

- [ ] **Step 1: Update the description and version-file input docs**

In `~/git/sunstone/actions/compute-version/action.yml`, change the top-level `description:` (line 2-4) to:

```yaml
description: >
  Compute a semver prod tag from pyproject.toml, package.json, or a
  plain-text VERSION file's major.minor, auto-incrementing the patch
  number based on existing git tags.
```

And the `version-file` input `description:` (around line 8-11) to:

```yaml
    description: >
      Path to the file containing the version. Supports pyproject.toml
      (TOML), package.json (JSON), and a plain-text VERSION file (e.g.
      "0.1" or "0.1.0"). If omitted, auto-detects by looking for
      pyproject.toml, then package.json, then VERSION in the repo root.
```

- [ ] **Step 2: Extend auto-detection to fall back to `VERSION`**

Replace the auto-detect block (currently):

```bash
        # Auto-detect version file if not specified
        if [ -z "$VERSION_FILE" ]; then
          if [ -f "pyproject.toml" ]; then
            VERSION_FILE="pyproject.toml"
          elif [ -f "package.json" ]; then
            VERSION_FILE="package.json"
          else
            echo "::error::No version file specified and no pyproject.toml or package.json found"
            exit 1
          fi
        fi
```

with:

```bash
        # Auto-detect version file if not specified
        if [ -z "$VERSION_FILE" ]; then
          if [ -f "pyproject.toml" ]; then
            VERSION_FILE="pyproject.toml"
          elif [ -f "package.json" ]; then
            VERSION_FILE="package.json"
          elif [ -f "VERSION" ]; then
            VERSION_FILE="VERSION"
          else
            echo "::error::No version file specified and no pyproject.toml, package.json, or VERSION found"
            exit 1
          fi
        fi
```

- [ ] **Step 3: Add the plain-text case to the extraction switch**

Replace the `case "$VERSION_FILE" in ... esac` block with a switch on `basename` so a `VERSION` file works regardless of path, and add the new branch before the catch-all error:

```bash
        # Extract major.minor based on file type
        case "$(basename "$VERSION_FILE")" in
          *.toml)
            MAJOR_MINOR=$(python3 -c "
        import tomllib, pathlib
        d = tomllib.loads(pathlib.Path('$VERSION_FILE').read_text())
        v = d.get('project', d.get('tool', {}).get('poetry', {})).get('version', '')
        print('.'.join(v.split('.')[:2]))
        ")
            ;;
          *.json)
            MAJOR_MINOR=$(python3 -c "
        import json, pathlib
        v = json.loads(pathlib.Path('$VERSION_FILE').read_text())['version']
        print('.'.join(v.split('.')[:2]))
        ")
            ;;
          VERSION)
            MAJOR_MINOR=$(tr -d '[:space:]' < "$VERSION_FILE" | cut -d. -f1,2)
            ;;
          *)
            echo "::error::Unsupported file type: $VERSION_FILE (expected .toml, .json, or a plain-text VERSION file)"
            exit 1
            ;;
        esac
```

(Everything below this — the `MAJOR_MINOR` empty check, tag lookup, and outputs — is unchanged.)

- [ ] **Step 4: Verify the plain-text branch in isolation**

There's no test harness in this repo (composite actions, no bats/CI test suite — verified by inspection, none present). Verify the new shell logic directly:

```bash
cd /tmp && rm -rf compute-version-check && mkdir compute-version-check && cd compute-version-check
git init -q
echo "0.1" > VERSION
git add VERSION && git commit -q -m "init"
VERSION_FILE="VERSION"
MAJOR_MINOR=$(tr -d '[:space:]' < "$VERSION_FILE" | cut -d. -f1,2)
echo "MAJOR_MINOR=$MAJOR_MINOR"
LATEST_TAG=$(git tag --list "v${MAJOR_MINOR}.*" --sort=-v:refname | head -1)
[ -z "$LATEST_TAG" ] && echo "PROD_TAG=v${MAJOR_MINOR}.0"
cd / && rm -rf /tmp/compute-version-check
```

Expected: `MAJOR_MINOR=0.1` then `PROD_TAG=v0.1.0`.

- [ ] **Step 5: Commit on a feature branch**

```bash
cd ~/git/sunstone/actions
git switch -c compute-version-plain-text-support
git add compute-version/action.yml
git commit -m "compute-version: support plain-text VERSION files"
```

---

### Task 2: `validate-kustomize@v1` — validate flat overlay roots, not just per-component subdirs

The action currently globs `${overlay-base}/${env}/*/` — only per-component subdirectories (e.g. `deploy/overlays/prod/migrations/`). A flat overlay like worklode's `deploy/overlays/hzdev/kustomization.yaml` (no subdirectories) never matches that glob, so the action silently reports "All overlays validated successfully" having validated **zero** overlays. Fix it to also build the env directory itself when it has a `kustomization.yaml` directly in it.

**Files:**
- Modify: `~/git/sunstone/actions/validate-kustomize/action.yml`

- [ ] **Step 1: Replace the validation loop**

Replace the body of the `run:` block (lines 22-40) with:

```bash
        set -euo pipefail
        ERRORS=0
        for env in ${{ inputs.environments }}; do
          ENV_DIR="${{ inputs.overlay-base }}/${env}"
          [ -d "$ENV_DIR" ] || continue

          # Flat layout: the env dir itself is a kustomization root.
          if [ -f "${ENV_DIR}/kustomization.yaml" ] || [ -f "${ENV_DIR}/kustomization.yml" ]; then
            echo "Validating ${ENV_DIR}..."
            if ! kustomize build "$ENV_DIR" > /dev/null; then
              echo "::error::Failed to build ${ENV_DIR}"
              ERRORS=$((ERRORS + 1))
            fi
          fi

          # Per-component subdir layout (e.g. app/, migrations/, db/).
          for component in "${ENV_DIR}"/*/; do
            [ -d "$component" ] || continue
            echo "Validating ${component}..."
            if ! kustomize build "$component" > /dev/null; then
              echo "::error::Failed to build ${component}"
              ERRORS=$((ERRORS + 1))
            fi
          done
        done
        if [ "$ERRORS" -gt 0 ]; then
          echo "::error::${ERRORS} overlay(s) failed validation"
          exit 1
        fi
        echo "All overlays validated successfully."
```

This is additive: existing per-component-subdir repos (e.g. `payload-cms`) keep validating exactly as before. Repos with a flat root *and* a subdir (e.g. `trusthere`'s `deploy/overlays/prod/` + `deploy/overlays/prod/migrations/`) now validate both instead of only the subdir — closes a real gap, not a behavior change to guard against.

- [ ] **Step 2: Verify locally against a throwaway flat overlay**

```bash
cd /tmp && rm -rf validate-kustomize-check && mkdir -p validate-kustomize-check/deploy/overlays/hzdev
cd validate-kustomize-check
cat > deploy/overlays/hzdev/kustomization.yaml <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
EOF
cat > deploy/overlays/hzdev/namespace.yaml <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: check
EOF
kustomize build deploy/overlays/hzdev > /dev/null && echo "flat overlay builds OK"
cd / && rm -rf /tmp/validate-kustomize-check
```

(Requires `kustomize` on PATH — `brew install kustomize` if missing. If genuinely unavailable, skip this manual check; Task 8's real `kustomize build` runs against worklode's actual overlays are the authoritative check.)

Expected: `flat overlay builds OK`.

- [ ] **Step 3: Commit on the same feature branch**

```bash
cd ~/git/sunstone/actions
git add validate-kustomize/action.yml
git commit -m "validate-kustomize: also validate flat overlay roots"
```

---

### Task 3: Push and open the actions repo PR

- [ ] **Step 1: Push and open a PR**

```bash
cd ~/git/sunstone/actions
git push -u origin compute-version-plain-text-support
gh pr create --title "compute-version: VERSION file support; validate-kustomize: flat overlays" --body "$(cat <<'EOF'
## Summary
- compute-version@v1: support a plain-text VERSION file (major.minor extraction) alongside pyproject.toml/package.json, for Go repos with neither.
- validate-kustomize@v1: also build the env directory itself when it has a kustomization.yaml directly in it, not just per-component subdirectories. Flat-overlay repos (e.g. worklode's deploy/overlays/hzdev/) previously validated zero overlays silently.

## Test plan
- [ ] Manual shell verification of both branches (see PR description / commit messages)
- [ ] Merge with a bump-patch label so v1 slides onto the new commit
EOF
)"
```

- [ ] **Step 2: Stop — do not merge**

This PR touches a shared action consumed by every Sunstone app repo. **Do not merge it as part of this plan.** Report the PR URL back and wait for review/merge. `promote-hzprod.yml` (Task 15) depends on the merge (see Phase A dependency note above) but nothing else in this plan is blocked by it.

---

## Phase B — `worklode`: `deploy/` tree

### Task 4: `VERSION` file

**Files:**
- Create: `VERSION`

- [ ] **Step 1: Create the file**

```
0.1
```

- [ ] **Step 2: Commit**

```bash
git add VERSION
git commit -m "Add VERSION file for semver prod promotion"
```

---

### Task 5: `deploy/base/` — namespace, storage, config, service

**Files:**
- Create: `deploy/base/namespace.yaml`
- Create: `deploy/base/pvc.yaml`
- Create: `deploy/base/configmap.yaml`
- Create: `deploy/base/service.yaml`

- [ ] **Step 1: `deploy/base/namespace.yaml`**

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: worklode
```

- [ ] **Step 2: `deploy/base/pvc.yaml`**

SQLite (`/data/wt.db`, per the Dockerfile's `CMD`) needs a real writable volume — irreplaceable data, so the Retain-policy storage class.

```yaml
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: worklode-data
  namespace: worklode
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: hcloud-volumes-retain
  resources:
    requests:
      storage: 2Gi
```

- [ ] **Step 3: `deploy/base/configmap.yaml`**

Only non-secret config lives here. `WT_CLUSTER_ENV_MAP` is a cluster-name-to-environment mapping, not a credential — see `internal/cmd/serve.go`. Overlays override it per environment.

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: worklode-config
  namespace: worklode
data:
  WT_CLUSTER_ENV_MAP: ""
```

- [ ] **Step 4: `deploy/base/service.yaml`**

```yaml
---
apiVersion: v1
kind: Service
metadata:
  name: worklode
  namespace: worklode
spec:
  selector:
    app: worklode
  ports:
    - name: http
      port: 80
      targetPort: http
```

- [ ] **Step 5: Commit**

```bash
git add deploy/base/namespace.yaml deploy/base/pvc.yaml deploy/base/configmap.yaml deploy/base/service.yaml
git commit -m "deploy: add base namespace/storage/config/service"
```

---

### Task 5B: Move migrations out of the Go binary into `deploy/base/migrations/`

worklode currently embeds its SQL migrations via `//go:embed migrations/*.sql` in `internal/store/store.go`, applying them automatically on every `store.Open()` call (both `wt serve` and the existing-but-redundant `wt migrate` command). Per explicit user decision, this moves to the decoupled pattern: migrations become the deploy tree's responsibility (`deploy/base/migrations/`, applied by a Kubernetes initContainer via a ConfigMap — added in Task 6), and `store.Open()` no longer self-migrates.

**This is a real behavior change, not just a file move** — six existing test files rely on `store.Open()` implicitly leaving a migrated database, and local dev via `docker compose up` relies on `wt serve` self-migrating on first boot. Both need explicit fixes, not just the Go source change.

**Files:**
- Move: `internal/store/migrations/*.sql` → `deploy/base/migrations/*.sql` (4 files: `0001_init.up.sql`, `0001_init.down.sql`, `0002_actor_admin.up.sql`, `0002_actor_admin.down.sql`)
- Modify: `internal/store/store.go`
- Create: `internal/store/testhelpers.go`
- Modify: `internal/cmd/migrate.go`
- Modify: `internal/api/server_test.go`, `internal/cli/client_test.go`, `internal/hooks/flux_test.go` (two call sites), `internal/hooks/github_test.go`, `e2e/smoke_test.go` — six `store.Open(...)` call sites total, found via `grep -rn "store\.Open(" --include=*.go .`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Move the SQL files**

```bash
mkdir -p deploy/base/migrations
git mv internal/store/migrations/0001_init.up.sql deploy/base/migrations/0001_init.up.sql
git mv internal/store/migrations/0001_init.down.sql deploy/base/migrations/0001_init.down.sql
git mv internal/store/migrations/0002_actor_admin.up.sql deploy/base/migrations/0002_actor_admin.up.sql
git mv internal/store/migrations/0002_actor_admin.down.sql deploy/base/migrations/0002_actor_admin.down.sql
rmdir internal/store/migrations
```

- [ ] **Step 2: `internal/store/store.go` — drop the embed, make `Migrate` take a path**

Remove the `"embed"` import and the `//go:embed migrations/*.sql` / `var migrationsFS embed.FS` block entirely. Add these imports instead:

```go
	"github.com/golang-migrate/migrate/v4/source"
	_ "github.com/golang-migrate/migrate/v4/source/file"
```

Change `Open` to no longer self-migrate — remove the `if err := s.Migrate(); err != nil { ... }` block, so it becomes:

```go
// Open opens (creating if necessary) the SQLite database at path. Callers
// are responsible for applying migrations (see Migrate) before relying on
// the schema being present — Open no longer does this implicitly.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	return &Store{db: db, nowFn: func() time.Time { return time.Now().UTC() }}, nil
}
```

Change `Migrate` to take a filesystem directory path instead of reading the embedded FS, using golang-migrate's `file` source (verified against the `github.com/golang-migrate/migrate/v4@v4.19.1` API already in `go.sum`: `source.Open(url string) (source.Driver, error)`, with the `file` scheme registered by blank-importing `source/file`):

```go
// Migrate applies all pending migrations found as *.up.sql/*.down.sql files
// in migrationsPath. A database that is already up to date is not an error.
func (s *Store) Migrate(migrationsPath string) error {
	absPath, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("resolve migrations path %s: %w", migrationsPath, err)
	}
	src, err := source.Open("file://" + absPath)
	if err != nil {
		return fmt.Errorf("load migrations from %s: %w", absPath, err)
	}
	drv, err := migratesqlite.WithInstance(s.db, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("init migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("file", src, "sqlite", drv)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
```

Add `"path/filepath"` to the import block (needed for `filepath.Abs`). Leave `migratesqlite`, `migrate`, `errors`, `fmt`, `time`, `database/sql`, `modernc.org/sqlite` imports as they are today — only the source changes, not the database driver wiring.

- [ ] **Step 3: `internal/store/testhelpers.go` — path helper for tests in other packages**

Tests live in `internal/api`, `internal/cli`, `internal/hooks`, and `e2e` — none of them can reliably hard-code a relative path to `deploy/base/migrations` (different depths from repo root). This helper resolves it from its own source location instead, so it works regardless of which package's test binary calls it. It must NOT be a `_test.go` file (those aren't importable across packages).

```go
package store

import (
	"path/filepath"
	"runtime"
)

// MigrationsDirForTests returns the absolute path to deploy/base/migrations,
// resolved relative to this source file so it works no matter which
// package's test binary calls it. Tests that need a migrated database call
// Open then Migrate(store.MigrationsDirForTests()).
func MigrationsDirForTests() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "deploy", "base", "migrations")
}
```

- [ ] **Step 4: Fix every `store.Open(...)` call site**

Run `grep -rn "store\.Open(" --include=*.go .` to confirm the full list (expected: `internal/api/server_test.go`, `internal/cli/client_test.go`, `internal/hooks/flux_test.go` ×2, `internal/hooks/github_test.go`, `e2e/smoke_test.go` — 6 sites; `internal/cmd/migrate.go` and `internal/cmd/serve.go` are handled separately in Step 5, not here).

For each test call site, immediately after the existing `store.Open(...)` + error check, add a call to apply migrations before the store is used, e.g.:

```go
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(store.MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
```

Match each site's existing error-handling idiom (`t.Fatalf`, `require.NoError`, etc. — whatever that file already uses) rather than introducing a new one. If a helper function wraps `store.Open` and is called from multiple tests in the same file, add the `Migrate` call inside that helper once rather than at every call site.

- [ ] **Step 5: `internal/cmd/migrate.go` — add `--migrations-path`**

```go
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func newMigrateCmd() *cobra.Command {
	var dbPath, migrationsPath string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Migrate(migrationsPath); err != nil {
				return err
			}
			cmd.Println("migrations applied")
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite database file")
	cmd.MarkFlagRequired("db")
	cmd.Flags().StringVar(&migrationsPath, "migrations-path", "", "path to the directory containing *.up.sql/*.down.sql migration files")
	cmd.MarkFlagRequired("migrations-path")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMigrateCmd())
}
```

`internal/cmd/serve.go` needs no change — it never called `Migrate` directly; it relied on `Open`'s old implicit behavior, which is exactly what Step 2 removed. `wt serve` now expects the schema to already exist (applied by the Task 6 initContainer in Kubernetes, or by the `migrate` compose service added in Step 6 for local dev).

- [ ] **Step 6: `docker-compose.yml` — apply migrations before `tracker` starts**

Add a one-shot `migrate` service ahead of `tracker`, gated with `depends_on: condition: service_completed_successfully` (Compose v2). Insert it as the first service, and add the `depends_on` block to the existing `tracker` service (everything else in `tracker` stays the same — do not reorder or remove existing keys):

```yaml
services:
  migrate:
    build: .
    command: ["migrate", "--db", "/data/wt.db", "--migrations-path", "/migrations"]
    volumes:
      - ./data:/data
      - ./deploy/base/migrations:/migrations:ro

  tracker:
    build: .
    depends_on:
      migrate:
        condition: service_completed_successfully
    ports:
      # Loopback-only by default: the web board and /metrics have no auth
      # (bearer tokens cover /api/v1 only). Change to "8080:8080" — or put a
      # TLS-terminating reverse proxy in front — to expose it; see README
      # "Network exposure".
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data:/data
    environment:
      # ${VAR:-} passes the var through when set on the host, empty otherwise.
      # WT_BOOTSTRAP_TOKEN must be set on first run to create the admin actor;
      # see README "Quickstart".
      WT_BOOTSTRAP_TOKEN: ${WT_BOOTSTRAP_TOKEN:-}
      WT_GITHUB_WEBHOOK_SECRET: ${WT_GITHUB_WEBHOOK_SECRET:-}
      WT_FLUX_WEBHOOK_SECRET: ${WT_FLUX_WEBHOOK_SECRET:-}
      WT_CLUSTER_ENV_MAP: ${WT_CLUSTER_ENV_MAP:-}
    restart: unless-stopped
    # No healthcheck: the image is distroless (no shell, no curl/wget), so
    # there is no in-container binary to run an HTTP probe with. Check
    # liveness from the host instead: curl http://localhost:8080/healthz.
```

(The `litestream` service below `tracker` in the existing file is unchanged — leave it exactly as is.)

- [ ] **Step 7: Verify**

```bash
go build ./...
go vet ./...
gofmt -l .
go test -race -count=1 ./...
go test -race -count=1 -tags e2e ./e2e/
docker compose config --quiet && echo "compose config OK"
```

Expected: everything passes. If Docker is actually available in this environment, also try `docker compose up -d && curl -sf http://localhost:8080/healthz && docker compose down` as a real end-to-end check; if Docker isn't available, `docker compose config --quiet` is sufficient and note in your report that the full run wasn't attempted.

- [ ] **Step 8: Commit**

```bash
git add internal/store internal/cmd/migrate.go docker-compose.yml deploy/base/migrations internal/api/server_test.go internal/cli/client_test.go internal/hooks/flux_test.go internal/hooks/github_test.go e2e/smoke_test.go
git commit -m "store: decouple migrations from the binary, move SQL to deploy/base/migrations"
```

(Adjust the `git add` file list if Step 4 touched different/additional files than listed above — add exactly what changed.)

---

### Task 6: `deploy/base/` — Deployment, migration initContainer, and Ingress

**Files:**
- Create: `deploy/base/deployment.yaml`
- Create: `deploy/base/ingress.yaml`
- Create: `deploy/base/kustomization.yaml`

**Depends on Task 5B being complete** — this task's `kustomization.yaml` generates a ConfigMap from `deploy/base/migrations/*.sql`, which only exists after Task 5B's `git mv`.

- [ ] **Step 1: `deploy/base/deployment.yaml`**

`gcr.io/distroless/static-debian12:nonroot` (the Dockerfile's runtime base) runs as UID/GID `65532` by convention — set it explicitly rather than relying on the image default. `strategy: Recreate` (not `RollingUpdate`) matters here: the SQLite file on the `ReadWriteOnce` PVC has a single writer, so two pods must never run against it at once during a rollout.

The `migrate` initContainer runs in the same Pod as the app (not a separate Flux-staged Job) deliberately: SQLite lives on a `ReadWriteOnce` volume, and a same-pod initContainer is the only way to apply migrations against it without a second pod fighting for the same RWO attachment. It uses the exact same app image (so `/wt migrate` runs the identical code path as `/wt serve`), reading SQL from a ConfigMap-mounted directory (`deploy/base/kustomization.yaml`'s `configMapGenerator`, Step 3 below) instead of an embedded FS.

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worklode
  namespace: worklode
  labels:
    app: worklode
spec:
  replicas: 1
  selector:
    matchLabels:
      app: worklode
  strategy:
    # SQLite on a ReadWriteOnce volume has one writer. Recreate avoids two
    # pods racing for the same volume/file lock during a rollout.
    type: Recreate
  template:
    metadata:
      labels:
        app: worklode
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8080"
        prometheus.io/path: "/metrics"
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
      imagePullSecrets:
        - name: ghcr-pull-secret
      initContainers:
        - name: migrate
          image: ghcr.io/sunstoneinstitute/worklode:latest
          command: ["/wt", "migrate", "--db", "/data/wt.db", "--migrations-path", "/migrations"]
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "256Mi"
              cpu: "500m"
          volumeMounts:
            - name: data
              mountPath: /data
            - name: migrations
              mountPath: /migrations
              readOnly: true
      containers:
        - name: worklode
          image: ghcr.io/sunstoneinstitute/worklode:latest
          imagePullPolicy: IfNotPresent
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          ports:
            - name: http
              containerPort: 8080
          envFrom:
            - configMapRef:
                name: worklode-config
            - secretRef:
                name: worklode-secrets
          resources:
            requests:
              memory: "128Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "500m"
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3
          volumeMounts:
            - name: data
              mountPath: /data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: worklode-data
        - name: migrations
          configMap:
            name: worklode-migrations
```

- [ ] **Step 2: `deploy/base/ingress.yaml`**

Placeholder hostname — each overlay patches it to the real one. Public `nginx` ingress class (not the internal-only router): GitHub Cloud must reach `/hooks/github`, and this mirrors how `flux-webhook.hzdev.sunstoneinstitute.ai` is exposed for the same reason.

```yaml
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: worklode-ingress
  namespace: worklode
  annotations:
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    nginx.ingress.kubernetes.io/force-ssl-redirect: "true"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - worklode.example.com
      secretName: worklode-tls
  rules:
    - host: worklode.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: worklode
                port:
                  number: 80
```

- [ ] **Step 3: `deploy/base/kustomization.yaml`**

The `configMapGenerator` turns `deploy/base/migrations/*.sql` into a ConfigMap named `worklode-migrations` (content-hash-suffixed by kustomize, so the Deployment's `configMap.name: worklode-migrations` reference is automatically rewritten to the hashed name wherever kustomize sees it — no manual wiring needed). List the migration files explicitly; if a new migration pair is added later, add its two files here too.

```yaml
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
  - pvc.yaml
  - configmap.yaml
  - deployment.yaml
  - service.yaml
  - ingress.yaml
configMapGenerator:
  - name: worklode-migrations
    files:
      - migrations/0001_init.up.sql
      - migrations/0001_init.down.sql
      - migrations/0002_actor_admin.up.sql
      - migrations/0002_actor_admin.down.sql
```

- [ ] **Step 4: Commit**

```bash
git add deploy/base/deployment.yaml deploy/base/ingress.yaml deploy/base/kustomization.yaml
git commit -m "deploy: add base Deployment (with migrate initContainer), Ingress, kustomization"
```

---

### Task 7: `deploy/overlays/hzdev/` and `deploy/overlays/hzprod/`

**Files:**
- Create: `deploy/overlays/hzdev/kustomization.yaml`
- Create: `deploy/overlays/hzdev/externalsecret-worklode-secrets.yaml`
- Create: `deploy/overlays/hzprod/kustomization.yaml`
- Create: `deploy/overlays/hzprod/externalsecret-worklode-secrets.yaml`

Both overlays are structurally identical; only the hostname and the 1Password `ClusterSecretStore` name differ (hzprod's store won't exist until a real hzprod cluster is provisioned — the overlay is still valid Kustomize source, it just has no cluster consuming it yet, per the plan's goal).

- [ ] **Step 1: `deploy/overlays/hzdev/kustomization.yaml`**

The `sunstone.institute/ghcr-pull: "true"` namespace label opts into the cluster-wide `ghcr-pull` `ClusterExternalSecret` fan-out (`provisioning/clusters/hzdev/ghcr-pull/`) — no per-app pull-secret plumbing needed. The `images:` entry has no `newName` override (worklode's image path is the same in every environment); it exists purely so `update-deploy-branch@v1`/`promote-images@v1` have something to `yq`-pin the tag on.

```yaml
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: worklode
resources:
  - ../../base
  - externalsecret-worklode-secrets.yaml
patches:
  - patch: |-
      apiVersion: v1
      kind: Namespace
      metadata:
        name: worklode
        labels:
          sunstone.institute/ghcr-pull: "true"
  - target:
      kind: Ingress
      name: worklode-ingress
    patch: |-
      - op: replace
        path: /spec/tls/0/hosts
        value:
          - worklode.hzdev.sunstoneinstitute.ai
      - op: replace
        path: /spec/rules/0/host
        value: worklode.hzdev.sunstoneinstitute.ai
images:
  - name: ghcr.io/sunstoneinstitute/worklode
    newTag: latest
```

- [ ] **Step 2: `deploy/overlays/hzdev/externalsecret-worklode-secrets.yaml`**

```yaml
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: worklode-secrets
  namespace: worklode
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: onepassword-hzdev-worklode
  target:
    name: worklode-secrets
    creationPolicy: Owner
  data:
    - secretKey: WT_BOOTSTRAP_TOKEN
      remoteRef:
        key: worklode-secrets
        property: WT_BOOTSTRAP_TOKEN
    - secretKey: WT_GITHUB_WEBHOOK_SECRET
      remoteRef:
        key: worklode-secrets
        property: WT_GITHUB_WEBHOOK_SECRET
    - secretKey: WT_FLUX_WEBHOOK_SECRET
      remoteRef:
        key: worklode-secrets
        property: WT_FLUX_WEBHOOK_SECRET
```

- [ ] **Step 3: `deploy/overlays/hzprod/kustomization.yaml`**

```yaml
---
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: worklode
resources:
  - ../../base
  - externalsecret-worklode-secrets.yaml
patches:
  - patch: |-
      apiVersion: v1
      kind: Namespace
      metadata:
        name: worklode
        labels:
          sunstone.institute/ghcr-pull: "true"
  - target:
      kind: Ingress
      name: worklode-ingress
    patch: |-
      - op: replace
        path: /spec/tls/0/hosts
        value:
          - worklode.hzprod.sunstoneinstitute.ai
      - op: replace
        path: /spec/rules/0/host
        value: worklode.hzprod.sunstoneinstitute.ai
images:
  - name: ghcr.io/sunstoneinstitute/worklode
    newTag: latest
```

- [ ] **Step 4: `deploy/overlays/hzprod/externalsecret-worklode-secrets.yaml`**

```yaml
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: worklode-secrets
  namespace: worklode
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: onepassword-hzprod-worklode
  target:
    name: worklode-secrets
    creationPolicy: Owner
  data:
    - secretKey: WT_BOOTSTRAP_TOKEN
      remoteRef:
        key: worklode-secrets
        property: WT_BOOTSTRAP_TOKEN
    - secretKey: WT_GITHUB_WEBHOOK_SECRET
      remoteRef:
        key: worklode-secrets
        property: WT_GITHUB_WEBHOOK_SECRET
    - secretKey: WT_FLUX_WEBHOOK_SECRET
      remoteRef:
        key: worklode-secrets
        property: WT_FLUX_WEBHOOK_SECRET
```

- [ ] **Step 5: Commit**

```bash
git add deploy/overlays
git commit -m "deploy: add hzdev and hzprod overlays"
```

---

### Task 8: Verify the Kustomize tree builds

**Files:** none (verification only)

- [ ] **Step 1: Build both overlays**

```bash
kustomize build deploy/overlays/hzdev > /dev/null && echo "hzdev OK"
kustomize build deploy/overlays/hzprod > /dev/null && echo "hzprod OK"
```

Expected: `hzdev OK` then `hzprod OK`. If `kustomize` isn't installed: `brew install kustomize`.

- [ ] **Step 2: Spot-check the rendered Deployment image and namespace**

```bash
kustomize build deploy/overlays/hzdev | yq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].image'
kustomize build deploy/overlays/hzdev | yq 'select(.kind == "Namespace") | .metadata.labels'
```

Expected: `ghcr.io/sunstoneinstitute/worklode:latest` and a map containing `sunstone.institute/ghcr-pull: "true"`.

No commit — this is a read-only check.

---

## Phase C — `worklode`: CI restructure

### Task 9: `.github/dependabot.yml`

worklode has `go.mod` (gomod), a `Dockerfile` (docker), and now `.github/workflows/*.yml` (github-actions) — no `pyproject.toml`/`package.json`, so no `uv`/`npm` ecosystem.

**Files:**
- Create: `.github/dependabot.yml`

- [ ] **Step 1: Create the file**

```yaml
version: 2
updates:
  - package-ecosystem: "github-actions"
    directory: "/"
    open-pull-requests-limit: 5
    groups:
      github-actions-updates:
        applies-to: version-updates
        dependency-type: production
    schedule:
      interval: "weekly"
      day: "monday"
      time: "09:00"

  - package-ecosystem: "gomod"
    directory: "/"
    open-pull-requests-limit: 5
    schedule:
      interval: "weekly"
      day: "monday"
      time: "09:00"

  - package-ecosystem: "docker"
    directory: "/"
    open-pull-requests-limit: 5
    schedule:
      interval: "weekly"
      day: "monday"
      time: "09:00"
```

- [ ] **Step 2: Commit**

```bash
git add .github/dependabot.yml
git commit -m "Add dependabot config for github-actions, gomod, docker"
```

---

### Task 10: Component workflow `_lint.yml`

**Files:**
- Create: `.github/workflows/_lint.yml`

- [ ] **Step 1: Create the file**

Split out of the current `ci.yml` `test` job's `gofmt`/`go vet` steps.

```yaml
name: lint

on:
  workflow_call:

permissions:
  contents: read

jobs:
  lint:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      - uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0
        with:
          go-version-file: go.mod
          cache: false
      - name: Go cache paths
        id: go-cache
        run: |
          echo "mod=$(go env GOMODCACHE)" >> "$GITHUB_OUTPUT"
          echo "build=$(go env GOCACHE)" >> "$GITHUB_OUTPUT"
      - name: Restore Go cache
        uses: actions/cache/restore@0057852bfaa89a56745cba8c7296529d2fc39830 # v4.3.0
        with:
          path: |
            ${{ steps.go-cache.outputs.mod }}
            ${{ steps.go-cache.outputs.build }}
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-
      - name: gofmt
        run: |
          out=$(gofmt -l .)
          if [ -n "$out" ]; then
            echo "gofmt needs to be run on:" >&2
            echo "$out" >&2
            exit 1
          fi
      - name: go vet
        run: go vet ./...
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/_lint.yml
git commit -m "ci: add lint component workflow"
```

---

### Task 11: Component workflow `_test.yml`

**Files:**
- Create: `.github/workflows/_test.yml`

- [ ] **Step 1: Create the file**

Ported from `ci.yml`'s `test` job (build/test/e2e + cache save), unchanged in behavior.

```yaml
name: test

on:
  workflow_call:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      - uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0
        with:
          go-version-file: go.mod
          cache: false
      - name: Go cache paths
        id: go-cache
        run: |
          echo "mod=$(go env GOMODCACHE)" >> "$GITHUB_OUTPUT"
          echo "build=$(go env GOCACHE)" >> "$GITHUB_OUTPUT"
      # Restore on every run; only main writes the cache (see the save step below).
      # PRs read the shared cache but cannot poison it.
      - name: Restore Go cache
        id: go-cache-restore
        uses: actions/cache/restore@0057852bfaa89a56745cba8c7296529d2fc39830 # v4.3.0
        with:
          path: |
            ${{ steps.go-cache.outputs.mod }}
            ${{ steps.go-cache.outputs.build }}
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-
      - name: go build
        run: go build ./...
      - name: go test
        run: go test -race -count=1 ./...
      - name: e2e smoke test
        run: go test -race -count=1 -tags e2e ./e2e/
      # Only main writes the cache, and only when go.sum changed (exact-key miss).
      - name: Save Go cache
        if: github.ref == 'refs/heads/main' && steps.go-cache-restore.outputs.cache-hit != 'true'
        uses: actions/cache/save@0057852bfaa89a56745cba8c7296529d2fc39830 # v4.3.0
        with:
          path: |
            ${{ steps.go-cache.outputs.mod }}
            ${{ steps.go-cache.outputs.build }}
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/_test.yml
git commit -m "ci: add test component workflow"
```

---

### Task 12: Component workflow `_build-image.yml`

**Files:**
- Create: `.github/workflows/_build-image.yml`

- [ ] **Step 1: Create the file**

One component, two callers: `pr-checks.yml` calls it with `push: false` (build-only regression check, replaces today's `docker` job), `deploy-hzdev.yml` calls it with the `push: true` default (real publish to `ghcr.io/sunstoneinstitute/worklode`). Preserves the existing buildkit-cache-dance Go-cache-mount wiring from `ci.yml`'s `docker` job.

```yaml
name: build-image

on:
  workflow_call:
    inputs:
      push:
        description: Push the built image to ghcr.io
        type: boolean
        default: true
    outputs:
      image-tag:
        description: Git short SHA used as the image tag
        value: ${{ jobs.build.outputs.image-tag }}

permissions:
  contents: read
  packages: write

env:
  REGISTRY: ghcr.io/sunstoneinstitute
  APP: worklode

jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 15
    outputs:
      image-tag: ${{ steps.image.outputs.tag }}
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0

      - name: Set image path and tag
        id: image
        run: |
          echo "path=${{ env.REGISTRY }}/${{ env.APP }}" >> "$GITHUB_OUTPUT"
          echo "tag=$(git rev-parse --short HEAD)" >> "$GITHUB_OUTPUT"

      - name: Compute image tags
        id: tags
        run: |
          if [ "${{ inputs.push }}" = "true" ]; then
            {
              echo "list<<EOF"
              echo "${{ steps.image.outputs.path }}:${{ steps.image.outputs.tag }}"
              echo "${{ steps.image.outputs.path }}:latest"
              echo "EOF"
            } >> "$GITHUB_OUTPUT"
          else
            {
              echo "list<<EOF"
              echo "worklode:ci"
              echo "EOF"
            } >> "$GITHUB_OUTPUT"
          fi

      - name: Log in to GitHub Container Registry
        if: inputs.push
        uses: docker/login-action@74a5d142397b4f367a81961eba4e8cd7edddf772 # v3.4.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/setup-buildx-action@8d2750c68a42422c14e847fe6c8ac0403b4cbd6f # v3.12.0
        id: buildx

      # Persist the Dockerfile's Go cache mounts across runs. buildkit-cache-dance
      # extracts them into ./go-cache-mount, which actions/cache stores. Only
      # push runs write (skip-extraction otherwise); non-push runs read the
      # shared cache without poisoning it.
      - name: Restore Go cache mounts
        uses: actions/cache@0057852bfaa89a56745cba8c7296529d2fc39830 # v4.3.0
        with:
          path: go-cache-mount
          key: ${{ runner.os }}-docker-go-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-docker-go-

      - name: Inject Go cache into buildx
        uses: reproducible-containers/buildkit-cache-dance@5422eac04292c961a382e0f584ea0f03ad9da723 # v3.4.0
        with:
          builder: ${{ steps.buildx.outputs.name }}
          cache-map: |
            {
              "go-cache-mount/mod": "/go/pkg/mod",
              "go-cache-mount/build": "/root/.cache/go-build"
            }
          skip-extraction: ${{ !inputs.push }}

      - name: Build and push Docker image
        uses: docker/build-push-action@10e90e3645eae34f1e60eeb005ba3a3d33f178e8 # v6.19.2
        with:
          context: .
          push: ${{ inputs.push }}
          tags: ${{ steps.tags.outputs.list }}
          cache-from: type=gha,scope=docker
          cache-to: ${{ inputs.push && 'type=gha,mode=max,scope=docker' || '' }}
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/_build-image.yml
git commit -m "ci: add build-image component workflow"
```

---

### Task 13: Trigger workflow `pr-checks.yml`

**Files:**
- Create: `.github/workflows/pr-checks.yml`

- [ ] **Step 1: Create the file**

Note `environments: hzdev hzprod` is passed explicitly — the action's default (`dev prod`) wouldn't match either of worklode's overlay names.

```yaml
name: PR Checks

on:
  pull_request:

permissions:
  contents: read

concurrency:
  group: ci-pr-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  lint:
    uses: ./.github/workflows/_lint.yml

  test:
    uses: ./.github/workflows/_test.yml

  build-image:
    uses: ./.github/workflows/_build-image.yml
    with:
      push: false

  validate-kustomize:
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
      - uses: sunstoneinstitute/actions/validate-kustomize@a3e86ec36df392ef4364a00266d0e54e713bef40 # v1
        with:
          overlay-base: deploy/overlays
          environments: hzdev hzprod
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/pr-checks.yml
git commit -m "ci: add PR Checks trigger workflow"
```

---

### Task 14: Trigger workflow `deploy-hzdev.yml`

**Files:**
- Create: `.github/workflows/deploy-hzdev.yml`

- [ ] **Step 1: Create the file**

Workflow-level `permissions:` is the union of what `_build-image.yml` (`packages: write`) and `update-deploy-branch@v1` (`contents: write`, to push `last-deploy/hzdev`) need — GitHub intersects caller/callee permissions, so both must be granted here even though the components declare their own.

```yaml
name: Deploy to hzdev

on:
  push:
    branches: [main]

permissions:
  contents: write
  packages: write

concurrency:
  group: deploy-hzdev
  cancel-in-progress: false

jobs:
  build-image:
    uses: ./.github/workflows/_build-image.yml

  deploy:
    needs: build-image
    runs-on: ubuntu-latest
    environment: hzdev
    timeout-minutes: 10
    steps:
      - name: Checkout code
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
        with:
          fetch-depth: 0

      - name: Update deploy branch
        uses: sunstoneinstitute/actions/update-deploy-branch@a3e86ec36df392ef4364a00266d0e54e713bef40 # v1
        with:
          env: hzdev
          images: worklode
          registry: ghcr.io/sunstoneinstitute
          tag: ${{ needs.build-image.outputs.image-tag }}
          overlay-path: deploy/overlays/hzdev/kustomization.yaml

      - name: Output summary
        run: |
          {
            echo "### Deployment Summary"
            echo ""
            echo "**Environment:** hzdev"
            echo "**Image tag:** \`${{ needs.build-image.outputs.image-tag }}\`"
            echo "**Deploy branch:** \`last-deploy/hzdev\`"
          } >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/deploy-hzdev.yml
git commit -m "ci: add Deploy to hzdev trigger workflow"
```

---

### Task 15: Trigger workflow `promote-hzprod.yml`

**Files:**
- Create: `.github/workflows/promote-hzprod.yml`

- [ ] **Step 1: Create the file**

Same-registry promotion (both hzdev and hzprod publish under `ghcr.io/sunstoneinstitute`, unlike the GCP dev/prod pattern's separate per-environment Artifact Registry projects) — `from-registry` and `to-registry` are identical, only the tag changes (git-sha → semver). Per the Phase A dependency note, this won't succeed with `version-file: VERSION` until Task 3's actions-repo PR is merged.

```yaml
name: Promote to hzprod

# Promotes the hzdev image to hzprod without rebuilding. Uses crane to copy
# the byte-identical image within the same ghcr.io/sunstoneinstitute
# namespace, tagged with a computed semver version.
#
# hzprod has no cluster yet, so nothing consumes last-deploy/hzprod until
# one is provisioned and Flux is wired to watch it — this workflow just
# keeps the branch and image ready for when that happens.

on:
  workflow_dispatch:

permissions:
  contents: write
  packages: write

concurrency:
  group: promote-hzprod
  cancel-in-progress: false

env:
  REGISTRY: ghcr.io/sunstoneinstitute
  APP: worklode

jobs:
  promote:
    runs-on: ubuntu-latest
    environment: hzprod
    timeout-minutes: 15
    steps:
      - name: Checkout code
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
        with:
          fetch-depth: 0

      - name: Log in to GitHub Container Registry
        uses: docker/login-action@74a5d142397b4f367a81961eba4e8cd7edddf772 # v3.4.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Promote image to hzprod
        id: promote
        uses: sunstoneinstitute/actions/promote-images@a3e86ec36df392ef4364a00266d0e54e713bef40 # v1
        with:
          from-env: hzdev
          to-env: hzprod
          images: worklode
          from-registry: ${{ env.REGISTRY }}
          to-registry: ${{ env.REGISTRY }}
          version-file: VERSION
          overlay-path: deploy/overlays/hzprod/kustomization.yaml

      - name: Output summary
        run: |
          {
            echo "### Production Promotion Summary"
            echo ""
            echo "**hzdev tag promoted:** \`${{ steps.promote.outputs.from-tag }}\`"
            echo "**hzprod version:** \`${{ steps.promote.outputs.to-tag }}\`"
            echo "**Deploy branch updated:** \`last-deploy/hzprod\`"
          } >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/promote-hzprod.yml
git commit -m "ci: add Promote to hzprod trigger workflow"
```

---

### Task 16: Remove the old `ci.yml`

**Files:**
- Delete: `.github/workflows/ci.yml`

- [ ] **Step 1: Delete and commit**

```bash
git rm .github/workflows/ci.yml
git commit -m "ci: remove monolithic ci.yml, superseded by trigger/component workflows"
```

- [ ] **Step 2: Verify the new workflow set**

```bash
ls .github/workflows/
```

Expected: `_build-image.yml  _lint.yml  _test.yml  deploy-hzdev.yml  pr-checks.yml  promote-hzprod.yml`

- [ ] **Step 3: Push the branch and open a PR**

```bash
git push -u origin <branch-name>
gh pr create --title "Restructure CI to trigger/component pattern; add hzdev/hzprod deploy" --body "$(cat <<'EOF'
## Summary
- Split ci.yml into trigger workflows (PR Checks, Deploy to hzdev, Promote to hzprod) calling shared component workflows (_lint, _test, _build-image), per the Sunstone github-actions skill pattern.
- Images publish to ghcr.io/sunstoneinstitute/worklode (no GCP/WIF — hzdev has no Workload Identity Federation; ghcr.io is the org convention for Hetzner clusters).
- Added deploy/base + deploy/overlays/{hzdev,hzprod} Kustomize tree, VERSION file, dependabot.yml.
- Depends on sunstoneinstitute/actions#<PR-number-from-Task-3> for VERSION-file support in compute-version@v1 and flat-overlay support in validate-kustomize@v1 — merge that first (or PR Checks' validate-kustomize step will silently pass without validating anything, and Promote to hzprod won't work).

## Test plan
- [ ] PR Checks (lint, test, build-image push:false, validate-kustomize) pass on this PR
- [ ] After merge, Deploy to hzdev pushes an image and updates last-deploy/hzdev
- [ ] Manual: run Promote to hzprod once, confirm last-deploy/hzprod is created with a v0.1.0 tag
EOF
)"
```

This PR should wait for the Task 3 actions-repo PR to merge first, or `validate-kustomize` will run the *old* action version (silently validating nothing) and `Promote to hzprod` won't work until then.

---

## Phase D — `provisioning`: wire Flux for hzdev

### Task 17: GitRepository + Kustomization

**Files:**
- Create: `~/git/sunstone/provisioning/clusters/hzdev/flux-system/gotk-worklode-repo.yaml`
- Create: `~/git/sunstone/provisioning/clusters/hzdev/flux-system/worklode.yaml`
- Modify: `~/git/sunstone/provisioning/clusters/hzdev/flux-system/kustomization.yaml`

- [ ] **Step 1: `gotk-worklode-repo.yaml`**

```yaml
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: worklode
  namespace: flux-system
spec:
  interval: 1m0s
  provider: github
  ref:
    branch: last-deploy/hzdev
  secretRef:
    name: flux-github-app
  url: https://github.com/sunstoneinstitute/worklode
```

- [ ] **Step 2: `worklode.yaml`**

No `decryption:` block — hzdev uses 1Password/ESO, not SOPS (unlike the GCP dev/prod clusters), matching `trusthere.yaml`'s hzdev entry exactly.

```yaml
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: worklode
  namespace: flux-system
spec:
  interval: 10m0s
  timeout: 10m
  dependsOn:
  - name: infra-db
  - name: external-secrets-config
  sourceRef:
    kind: GitRepository
    name: worklode
  path: ./deploy/overlays/hzdev
  prune: true
  wait: true
  healthChecks:
    - apiVersion: apps/v1
      kind: Deployment
      name: worklode
      namespace: worklode
```

- [ ] **Step 3: Add both to `clusters/hzdev/flux-system/kustomization.yaml`**

In the `resources:` list, immediately after the existing `- gotk-data-platform-repo.yaml` / `- data-platform.yaml` pair and before `- notifications`, add:

```yaml
- gotk-worklode-repo.yaml
- worklode.yaml
```

- [ ] **Step 4: Verify the kustomization list is well-formed**

```bash
cd ~/git/sunstone/provisioning
kustomize build clusters/hzdev/flux-system > /dev/null && echo "hzdev flux-system OK"
```

Expected: `hzdev flux-system OK`. (This does not require cluster access — it's a pure YAML render.)

- [ ] **Step 5: Commit**

```bash
git add clusters/hzdev/flux-system/gotk-worklode-repo.yaml clusters/hzdev/flux-system/worklode.yaml clusters/hzdev/flux-system/kustomization.yaml
git commit -m "hzdev: register worklode GitRepository and Kustomization"
```

---

### Task 18: Per-app 1Password ClusterSecretStore chain

**Files:**
- Create: `~/git/sunstone/provisioning/clusters/hzdev/external-secrets-config/clustersecretstore-worklode.yaml`
- Create: `~/git/sunstone/provisioning/clusters/hzdev/external-secrets-config/externalsecret-op-connect-token-worklode.yaml`
- Modify: `~/git/sunstone/provisioning/clusters/hzdev/external-secrets-config/kustomization.yaml`

- [ ] **Step 1: `clustersecretstore-worklode.yaml`**

```yaml
---
# App-tier store: hzdev-app-worklode vault, usable only from the
# worklode namespace (pinned via the automatic kubernetes.io/metadata.name
# label).
apiVersion: external-secrets.io/v1
kind: ClusterSecretStore
metadata:
  name: onepassword-hzdev-worklode
spec:
  conditions:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: worklode
  provider:
    onepassword:
      connectHost: http://op-connect-egress.external-secrets.svc:8080
      vaults:
        hzdev-app-worklode: 1
      auth:
        secretRef:
          connectTokenSecretRef:
            name: op-connect-token-worklode
            key: token
            namespace: external-secrets
```

- [ ] **Step 2: `externalsecret-op-connect-token-worklode.yaml`**

```yaml
---
# One vault + token + store per app for granular access control. The
# platform store (onepassword-hzdev) syncs the Connect token for the
# hzdev-app-worklode vault; the app ClusterSecretStore above
# authenticates with it.
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: op-connect-token-worklode
  namespace: external-secrets
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: onepassword-hzdev
  target:
    name: op-connect-token-worklode
    creationPolicy: Owner
  data:
    - secretKey: token
      remoteRef:
        key: eso-connect-token-worklode
        property: credential
```

- [ ] **Step 3: Add both to `clusters/hzdev/external-secrets-config/kustomization.yaml`**

Append to the `resources:` list:

```yaml
- externalsecret-op-connect-token-worklode.yaml
- clustersecretstore-worklode.yaml
```

- [ ] **Step 4: Verify**

```bash
cd ~/git/sunstone/provisioning
kustomize build clusters/hzdev/external-secrets-config > /dev/null && echo "external-secrets-config OK"
```

- [ ] **Step 5: Commit**

```bash
git add clusters/hzdev/external-secrets-config/clustersecretstore-worklode.yaml clusters/hzdev/external-secrets-config/externalsecret-op-connect-token-worklode.yaml clusters/hzdev/external-secrets-config/kustomization.yaml
git commit -m "hzdev: add worklode per-app 1Password ClusterSecretStore"
```

**This step alone does not make secrets flow.** It authenticates the *store*, not the app's actual secret values — see the manual follow-up below (1Password vault `hzdev-app-worklode` + item `worklode-secrets` must exist with real `WT_BOOTSTRAP_TOKEN`/`WT_GITHUB_WEBHOOK_SECRET`/`WT_FLUX_WEBHOOK_SECRET` values before the `ExternalSecret` in `deploy/overlays/hzdev/` can sync).

---

### Task 19: Regenerate cascade notifications

**Files:**
- Modify (generated): `clusters/hzdev/flux-system/notifications/{cascade,receivers,providers,webhook-token-externalsecrets,alerts}.yaml`

- [ ] **Step 1: Run the generator**

Requires `op` CLI authenticated to the account holding vault `hzdev-cluster-platform` (per the user's global instruction: run it directly, don't pre-probe `op whoami`/`op signin`). This provisions a fresh webhook token for the new `worklode` node automatically (`op item edit`) since none exists yet.

```bash
cd ~/git/sunstone/provisioning
uv run scripts/generate-notifications.py hzdev
```

Expected: exits 0, and the five files under `clusters/hzdev/flux-system/notifications/` are rewritten to include a `worklode` node (new `Receiver`/`Provider`/`ExternalSecret`/`Alert`, and an `Alert` cascading from `external-secrets-config` — its dependency — to `worklode-receiver`).

- [ ] **Step 2: Review the diff**

```bash
git diff clusters/hzdev/flux-system/notifications/
```

Confirm the diff only adds `worklode`-related blocks; it should not touch any existing node's provider address (that would mean an existing token got rotated, which is a real behavior change worth double-checking before committing).

- [ ] **Step 3: Commit**

```bash
git add clusters/hzdev/flux-system/notifications/
git commit -m "hzdev: regenerate cascade notifications for worklode"
```

---

### Task 20: Push and open the provisioning PR

**Files:** none (git/gh operations only)

- [ ] **Step 1: Push and open a PR**

This touches live cluster GitOps state — **do not merge automatically.**

```bash
cd ~/git/sunstone/provisioning
git push -u origin <branch-name>
gh pr create --title "hzdev: wire up worklode Flux deployment" --body "$(cat <<'EOF'
## Summary
- Register worklode's GitRepository (tracking last-deploy/hzdev) and Kustomization (deploy/overlays/hzdev, depends on infra-db + external-secrets-config) on hzdev.
- Add the per-app 1Password ClusterSecretStore chain (onepassword-hzdev-worklode), mirroring trusthere.
- Regenerate cascade notifications to include the new worklode node.

## Before merging
- [ ] Create the 1Password vault `hzdev-app-worklode` and item `worklode-secrets` with WT_BOOTSTRAP_TOKEN / WT_GITHUB_WEBHOOK_SECRET / WT_FLUX_WEBHOOK_SECRET (see worklode repo's deploy PR for the ExternalSecret that consumes it) — otherwise the ExternalSecret in deploy/overlays/hzdev/ won't sync and the Kustomization will report NotReady.
- [ ] Confirm worklode's `Deploy to hzdev` workflow has run at least once, so last-deploy/hzdev exists (the GitRepository source will fail to resolve the branch otherwise).

## Test plan
- [ ] kustomize build clusters/hzdev/flux-system and clusters/hzdev/external-secrets-config both succeed (done pre-PR)
- [ ] After merge: kubectl -n flux-system get kustomization worklode shows Ready once 1Password is populated
EOF
)"
```

- [ ] **Step 2: Report the PR URL and stop.**

---

## Manual follow-ups (not automated by this plan)

These require the user directly — either because they mint real credentials, or because they're one-time GitHub/repo-settings actions this plan intentionally doesn't perform unattended:

1. **Merge the `sunstoneinstitute/actions` PR (Task 3)** with a `bump-patch` label, before merging worklode's CI PR (Task 16) — otherwise `validate-kustomize` silently validates nothing and `Promote to hzprod` fails.

2. **Create the 1Password vault and secrets for worklode on hzdev:**
   - Vault `hzdev-app-worklode`, granted to the hzdev 1Password Connect server (same process used for `hzdev-app-trusthere` — see `~/git/sunstone/provisioning`'s 1Password/ESO bootstrap docs for the exact `op vault create`/Connect-grant steps, since they touch live Connect server config).
   - Item `worklode-secrets` in that vault with fields `WT_BOOTSTRAP_TOKEN`, `WT_GITHUB_WEBHOOK_SECRET`, `WT_FLUX_WEBHOOK_SECRET` — generate with `openssl rand -hex 20`/`openssl rand -hex 32` per the README's guidance.
   - Item `flux-webhook-tokens` field `worklode` in vault `hzdev-cluster-platform` is handled automatically by Task 19's generator script — no manual step there.

3. **Configure the GitHub webhook** on `sunstoneinstitute/worklode` pointing at `https://worklode.hzdev.sunstoneinstitute.ai/hooks/github` once the app is live, using the `WT_GITHUB_WEBHOOK_SECRET` value from step 2 — see the app-deployment skill's `references/github-webhook-receiver.md` and this repo's README "GitHub App" section.

4. **First-push GHCR package visibility check**: after `Deploy to hzdev` runs once, confirm the `sunstone-ghcr-pull` bot account (or whichever principal backs the cluster's `ghcr-pull` `ClusterExternalSecret`) can read the new `ghcr.io/sunstoneinstitute/worklode` package — same check done when `trusthere`/`sunstone-cms` onboarded to hzdev.

5. **Merge order overall**: actions PR → worklode CI/deploy PR → confirm `Deploy to hzdev` runs green and `last-deploy/hzdev` exists → 1Password vault populated (step 2) → provisioning PR (Task 20). Flux's `Kustomization` will sit `NotReady` (missing source branch or missing secret) until both the deploy branch and the 1Password secret exist — that's expected, not a bug to chase.

---

## Self-review notes

- **Spec coverage:** trigger/component CI restructure ✓ (Tasks 10-16); `deploy/` dir ✓ (Tasks 5-8); merges to main deploy to hzdev via `last-deploy/hzdev` ✓ (Task 14, 17); prod-promote workflow for hzprod with no consumer yet ✓ (Task 15, explicitly not wiring Flux for hzprod in Phase D); ghcr.io not GCP WIF, per explicit correction ✓ (Task 12, 14, 15 all use `ghcr.io/sunstoneinstitute`, no `google-github-actions/auth` anywhere).
- **Cross-repo consistency:** image path (`ghcr.io/sunstoneinstitute/worklode`), env names (`hzdev`/`hzprod`), and the `images:`/`registry:`/`overlay-path:` arguments passed to `update-deploy-branch@v1`/`promote-images@v1` are consistent between Task 12/14/15 (worklode CI) and Task 7 (deploy overlays) — the `images` value `worklode` matches the overlay's `images[].name` suffix (`ghcr.io/sunstoneinstitute/worklode` ends in `/worklode`), which is what the action's matcher requires.
- **Known gap fixed in passing:** `validate-kustomize@v1`'s subdir-only glob (Task 2) would otherwise make Task 13's PR-check step a false-green no-op for worklode's flat overlays.
