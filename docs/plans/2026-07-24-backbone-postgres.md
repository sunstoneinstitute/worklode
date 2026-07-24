# Execution Backbone on Postgres (spec 01) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Port the backbone from single-writer SQLite to Postgres (pgx v5), rebind leases from `session_id` to git-worktree identity, and rename the CLI `wl` → `lode` — implementing `docs/specs/worklode/01-execution-backbone.md`.

**Architecture:** The store keeps its shape (`internal/store`, `*sql.Tx`-typed functions, `RecordEvent` as sole write entry point) but runs on Postgres via `github.com/jackc/pgx/v5/stdlib` (`database/sql` driver name `"pgx"`), with a real connection pool replacing `SetMaxOpenConns(1)`. Claim correctness moves from global single-writer to `SELECT … FOR UPDATE` row locks + the `leases_active` unique-index backstop (READ COMMITTED). A fresh Postgres migration baseline replaces the SQLite migrations. Leases carry a `worktree` identity string (canonical form `<hostname>:<abs-worktree-root>`), no `session_id` anywhere.

**Tech Stack:** Go 1.26, pgx v5 (`stdlib`), golang-migrate (`database/pgx/v5` driver), Postgres 17 (docker-compose service locally, service container in CI, CNPG in-cluster).

**Settled decisions (do not re-litigate during execution):**
- Task IDs stay `WL-<n>`; PR-branch prefix stays `wl/<id>-<slug>`. The spec's `WT-<n>` mentions are stale branding (repo-wide WT→WL rename already happened). Worktree *directories* `wt/<id>-<slug>` arrive in the spec-05 plan, not here.
- CLI binary renames to `lode`; env vars rename `WL_*` → `LODE_*` with **no** fallback (only hzdev dev is deployed; coordinate the deploy overlay in the same PR). Config path `~/.config/worklode/config.toml` unchanged.
- Worktree identity: opaque string, canonical form `<hostname>:<abs-worktree-root>` (spec 01 recommendation). Backbone never parses it.
- Isolation: READ COMMITTED + `FOR UPDATE` + unique-index backstop (spec open Q3 → confirmed).
- Sweeper under multiple replicas: gate with `pg_try_advisory_lock` (spec open Q4 → advisory lock).
- Litestream (SQLite replication) is deleted — CNPG owns backups.
- Reopen already exists in code (`legalTransitions`, `wl task reopen`); Task 7 only verifies/completes it against the spec.

**Skills the executor should load when a task says so:** `golang-migrate:authoring` / `golang-migrate:test-roundtrip` / `golang-migrate:k8s-job`, `kubernetes` (CNPG), from the sunstone plugins.

---

### Task 1: Rename `wl` → `lode`

Pure mechanical rename, one commit, before any Postgres work so all later diffs read with final naming.

**Files:**
- Rename: `cmd/wl/` → `cmd/lode/` (package main stays)
- Modify: `internal/cmd/root.go` (Use: "wl" → "lode"; help text)
- Modify: everywhere `WL_SERVER`, `WL_TOKEN`, `WL_BOOTSTRAP_TOKEN`, `WL_GITHUB_WEBHOOK_SECRET`, `WL_FLUX_WEBHOOK_SECRET`, `WL_CLUSTER_ENV_MAP`, and any other `WL_*` env var is read or documented → `LODE_*` (grep `WL_` across `internal/`, `cmd/`, `README.md`, `docker-compose.yml`, `deploy/`, `.github/workflows/`, `e2e/`)
- Modify: `Dockerfile` (binary name), `.github/workflows/*.yml` (build paths/binary), `deploy/` manifests (command/args/env), `README.md`
- Do NOT touch: module path `github.com/sunstoneinstitute/worklode`, task-ID prefix `WL-`, branch prefix `wl/`, `~/.config/worklode/`

**Steps:**

- [x] **Step 1: Inventory.** Run `grep -rn "cmd/wl\b\|\"wl\"\|WL_" --include="*.go" --include="*.yml" --include="*.yaml" --include="*.md" --include="Dockerfile" . | grep -v "WL-" | grep -v "wl/"` and list every hit to change. (Careful: `WL-<n>` task ids and `wl/` branch refs stay.)
- [x] **Step 2: `git mv cmd/wl cmd/lode`**, update `internal/cmd/root.go` `Use:`/examples, update Dockerfile/workflows/compose/deploy/README/env-var reads. Env reads live in `internal/cmd/serve.go`, `internal/cmd/root.go` (or wherever `os.Getenv("WL_` appears), `internal/cli/`.
- [x] **Step 3: Verify.** `go build ./... && go test ./...` (still SQLite at this point) — all green. `grep -rn "WL_" --include="*.go" .` returns nothing (except `WL-` id constants if any).
- [x] **Step 4: Commit** `git commit -m "Rename CLI wl -> lode, env vars WL_* -> LODE_*"`

### Task 2: Postgres migration baseline

Replace the three SQLite migrations with one fresh Postgres baseline implementing the spec-01 schema. Load `golang-migrate:authoring` and `golang-migrate:test-roundtrip` skills first.

**Files:**
- Delete: `deploy/base/migrations/0001_init.{up,down}.sql`, `0002_actor_admin.{up,down}.sql`, `0003_github_user_tokens.{up,down}.sql`
- Create: `deploy/base/migrations/0001_baseline.up.sql`, `0001_baseline.down.sql`

**Steps:**

- [x] **Step 1: Read the three old SQLite migrations** end to end — every table they create must exist in the new baseline (backbone tables per spec 01 §Data model; observed/auth tables carried over: `projects`, `project_repos`, `actors` (+ `admin` boolean from 0002), `tokens`, `github_user_tokens` (from 0003), `issues`, `pull_requests`, `ci_runs`, `reviews`, `artifacts`, `deployments`, `runtime_events`, `task_seq`).
- [x] **Step 2: Write `0001_baseline.up.sql`.** Conventions: `bigint GENERATED ALWAYS AS IDENTITY` PKs (where the SQLite schema had autoincrement ids), `timestamptz` for every timestamp, `boolean` for flags (`actors.admin`, `projects.deploy_gated`), `jsonb` for `events.payload` / `state_log.change` / any other JSON-text column, FKs as in the old schema. The backbone tables exactly per spec 01:

```sql
CREATE TABLE tasks (
    id         text PRIMARY KEY,                    -- WL-<n>
    project_id text NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    title      text NOT NULL,
    body       text,
    priority   text NOT NULL CHECK (priority IN ('critical','high','medium','low')),
    kind       text NOT NULL CHECK (kind IN ('feature','bug','chore','spec')),
    state      text NOT NULL CHECK (state IN
                 ('draft','ready','in_progress','in_review','done','abandoned')),
    created_by text REFERENCES actors(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE leases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text NOT NULL REFERENCES tasks(id)  ON DELETE RESTRICT,
    actor_id    text NOT NULL REFERENCES actors(id) ON DELETE RESTRICT,
    worktree    text NOT NULL,
    acquired_at timestamptz NOT NULL,
    renewed_at  timestamptz,
    expires_at  timestamptz NOT NULL,
    released_at timestamptz
);
CREATE UNIQUE INDEX leases_active ON leases (task_id) WHERE released_at IS NULL;
CREATE UNIQUE INDEX leases_active_worktree ON leases (worktree) WHERE released_at IS NULL;

CREATE TABLE task_edges (
    from_task  text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    to_task    text NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
    type       text NOT NULL CHECK (type IN ('child_of','blocks')),
    created_at timestamptz NOT NULL,
    UNIQUE (from_task, to_task, type)
);

CREATE TABLE events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source      text NOT NULL CHECK (source IN ('github','flux','watcher','cli','system')),
    external_id text NOT NULL,
    type        text NOT NULL,
    payload     jsonb,
    received_at timestamptz NOT NULL,
    UNIQUE (source, external_id)
);

CREATE TABLE state_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_kind text NOT NULL,
    entity_id   text NOT NULL,
    change      jsonb NOT NULL,
    event_id    bigint NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    at          timestamptz NOT NULL
);

CREATE TABLE task_seq (id integer PRIMARY KEY CHECK (id = 1), next bigint NOT NULL);
INSERT INTO task_seq (id, next) VALUES (1, 1);
```

Carry the remaining tables over from the SQLite schema translated to the same conventions (keep every column and index; translate `TEXT` timestamps → `timestamptz`, JSON text → `jsonb`, int flags → `boolean`).
- [x] **Step 3: Write `0001_baseline.down.sql`** — `DROP TABLE … CASCADE` in reverse-dependency order (or a single `DROP TABLE a, b, c … CASCADE`).
- [x] **Step 4: Round-trip.** Per `golang-migrate:test-roundtrip`: against a scratch Postgres (`docker run --rm -d -e POSTGRES_PASSWORD=postgres -p 5499:5432 postgres:17`), run migrate up → down → up cleanly with the golang-migrate CLI (or a tiny Go test if the CLI isn't installed; Task 3's roundtrip test will also cover this permanently).
- [x] **Step 5: Commit** `git commit -m "Replace SQLite migrations with Postgres baseline (spec 01 schema)"` (build is expected red between Tasks 2–4 only if code references removed files — it doesn't; migrations are data, code still compiles).

### Task 3: Store on pgx — `Open`, `Migrate`, test infrastructure

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/testhelpers.go`
- Modify: `internal/store/errors.go`
- Modify: `docker-compose.yml` (add `postgres` service; repoint `migrate`/`worklode` services)
- Modify: `.github/workflows/_test.yml` (postgres service container + `TEST_POSTGRES_DSN`)
- Modify: `go.mod` (add `github.com/jackc/pgx/v5`; golang-migrate pgx driver; remove `modernc.org/sqlite` + migrate sqlite driver once Task 4 lands)

**Steps:**

- [x] **Step 1: Rewrite `store.Open`:**

```go
// Open opens a Postgres-backed store for the given postgres:// DSN.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Store{db: db, nowFn: func() time.Time { return time.Now().UTC() }}, nil
}
```

Imports: `_ "github.com/jackc/pgx/v5/stdlib"`. Rewrite `Migrate` with `migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"` (`WithInstance(s.db, &migratepgx.Config{})`, `NewWithInstance("file", src, "pgx5", drv)`). Update the package doc comment (no more single-writer).
- [x] **Step 2: Add unique-violation helper to `errors.go`:**

```go
// isUniqueViolation reports whether err is a Postgres unique-index violation
// (SQLSTATE 23505), the backstop for claim races and duplicate edges.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
```

- [x] **Step 3: Rewrite `testhelpers.go`** — per-test ephemeral database:

```go
// TestDSN returns the Postgres DSN test databases are created under.
// Default matches the docker-compose postgres service.
func TestDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
}

// OpenTestStore creates a uniquely named database, applies all migrations,
// and returns a Store bound to it. The database is dropped on cleanup.
// Skips the test if Postgres is unreachable and CI is not set.
func OpenTestStore(t *testing.T) *Store
```

Implementation: connect to `TestDSN()`, `CREATE DATABASE wl_test_<12-hex>`; build the per-test DSN by swapping the database path segment; `Open` it; `Migrate(MigrationsDirForTests())`; `t.Cleanup`: close store, `DROP DATABASE … WITH (FORCE)`. If the initial connect/ping fails: `t.Skipf` when `os.Getenv("CI") == ""`, `t.Fatalf` in CI (tests must not silently skip in CI). Keep `MigrationsDirForTests` as-is.
- [x] **Step 4: Add a store_test round-trip test** (replaces the ad-hoc check from Task 2):

```go
func TestMigrateRoundTrip(t *testing.T) {
	s := OpenTestStore(t) // up happened here
	// down to zero and back up again must both succeed
	if err := s.MigrateDown(MigrationsDirForTests()); err != nil { t.Fatal(err) }
	if err := s.Migrate(MigrationsDirForTests()); err != nil { t.Fatal(err) }
}
```

Add the small `MigrateDown` sibling of `Migrate` (calls `m.Down()`, tolerating `migrate.ErrNoChange`).
- [x] **Step 5: docker-compose:** add

```yaml
  postgres:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: postgres
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    restart: unless-stopped
```

Repoint `migrate` service command to `["migrate", "--dsn", "postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable", "--migrations-path", "/migrations"]` with `depends_on: postgres` (add a `pg_isready` healthcheck to the postgres service and `condition: service_healthy`); `worklode` service gets `LODE_DSN` env instead of the `/data` volume. **Delete the `litestream` service and `litestream.yml`.**
- [x] **Step 6: CI:** in `.github/workflows/_test.yml` add a `postgres:17` service container (env `POSTGRES_PASSWORD: postgres`, port 5432, `--health-cmd pg_isready` options) and export `TEST_POSTGRES_DSN` to the test step.
- [x] **Step 7:** `go build ./...` (store tests still red until Task 4 — that's expected; do not run the full suite yet). Commit `git commit -m "Store opens Postgres via pgx; ephemeral test databases; compose/CI postgres"`.

### Task 4: Port store queries to Postgres

Mechanical port of every `internal/store/*.go` file. **Porting recipe, applied uniformly:**

1. Placeholders `?` → `$1, $2, …` (positional, in argument order).
2. Timestamp scanning: columns are now `timestamptz` — scan `time.Time` directly (or `sql.NullTime` for nullable); delete every `time.Parse(time.RFC3339, …)` in scanners and every `.Format(time.RFC3339)` when writing (pass `time.Time` values straight through). Keep `.UTC().Truncate(time.Second)` on generated timestamps for output stability.
3. Boolean columns (`actors.admin`, `projects.deploy_gated`): scan/write Go `bool` (drop int conversions).
4. JSON columns: keep marshaling with `encoding/json`; pass `[]byte` to `jsonb` params (pgx handles it). A `nil` payload must be written as SQL `NULL`, not `[]byte("null")` — keep existing behavior.
5. Unique-violation checks (previously SQLite error-string/code checks in `leases.go` claim backstop and `task_edges` duplicate detection) → `isUniqueViolation(err)`.
6. `UPDATE task_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1` — valid Postgres, keep verbatim.
7. SQLite-isms to hunt: `INSERT OR IGNORE` → `ON CONFLICT DO NOTHING`; `INSERT OR REPLACE` → `ON CONFLICT … DO UPDATE`; `datetime(...)` functions → interval arithmetic or Go-side computation; `LIMIT -1` → drop.

**Files (port in this order, running that file's tests after each):**
- Modify: `internal/store/events.go` + `events_test.go` (RecordEvent is the keystone — `ON CONFLICT (source, external_id) DO NOTHING` + `RETURNING id`; on conflict re-select the existing id, `inserted=false`)
- Modify: `internal/store/tasks.go` + `tasks_test.go`
- Modify: `internal/store/leases.go` + `leases_test.go` (**including the worktree rebind — see Task 5, done together with this file**)
- Modify: `internal/store/actors.go`, `projects.go`, `github_tokens.go`, `inbox.go`, `changes.go`, `artifacts.go`, `runtime.go` + their tests
- Modify: every test file: replace the `Open(tempfile) + Migrate(...)` setup pattern with `OpenTestStore(t)`

**Steps:**

- [x] **Step 1:** Port `events.go`; update `events_test.go` to `OpenTestStore`; `go test ./internal/store/ -run TestRecordEvent -v` green (adjust run pattern to the file's actual test names).
- [x] **Step 2:** Port `tasks.go` (+`tasks_test.go`); tests for that file green.
- [x] **Step 3:** Port `leases.go` together with Task 5's worktree rebind (one edit pass over the file); its tests green after Task 5.
- [x] **Step 4:** Port the remaining store files + tests, one commit per coherent chunk.
- [x] **Step 5:** Drop `modernc.org/sqlite` and the migrate sqlite driver from `go.mod` (`go mod tidy`); `grep -rn "sqlite" --include="*.go" .` returns nothing.
- [x] **Step 6:** `go test ./internal/store/` fully green. Commit.

### Task 5: Worktree-bound leases (schema is done; code + API + CLI)

**Files:**
- Modify: `internal/store/leases.go`, `leases_test.go`
- Modify: `internal/api/lifecycle.go` (+ its test), any API types file with `session_id`
- Modify: `internal/cmd/task.go`, `internal/cli/client.go` (+ tests)
- Create: `internal/cli/worktree.go` (+ test)

**Steps:**

- [x] **Step 1: Store.** In `Lease` struct: `SessionID string` → `Worktree string`; `leaseColumns` and `scanLease` follow (`worktree` is `NOT NULL` — plain string, no NullString). `Claim(ctx, taskID, actorID, worktree string, ttl)` — same signature shape, renamed param, inserts `worktree`. `Renew`/`Release` keep `(taskID, actorID)` holder semantics (spec: holder is the actor; worktree is binding metadata). The `leases_active_worktree` index now enforces one active lease per worktree — claim maps its 23505 to `ErrLeased` regardless of which index fired.
- [x] **Step 2: Claim row lock.** First statement inside the claim apply-callback:

```go
var state string
err := tx.QueryRow(`SELECT state FROM tasks WHERE id = $1 FOR UPDATE`, taskID).Scan(&state)
// sql.ErrNoRows -> ErrNotFound
```

Then actor check, active-lease check (`ErrLeased`), `IsBlocked` (`ErrBlocked`), `Transition(tx, now, taskID, "ready", "in_progress", eventID)`, lease INSERT with `isUniqueViolation` → `ErrLeased` backstop. (Order per spec 01 §The claim transaction.)
- [x] **Step 3: Store tests.** Update existing lease tests for `Worktree`; add:

```go
func TestClaimSecondWorktreeSameTask(t *testing.T)   // second claim, different worktree -> ErrLeased
func TestClaimSameWorktreeSecondTask(t *testing.T)   // one worktree claiming a second task -> ErrLeased (leases_active_worktree)
```

- [x] **Step 4: API.** Claim request field `session_id` → `worktree` (required, non-empty → 400 if missing); `Lease` JSON: `session_id` → `worktree`. Update `internal/api` tests.
- [x] **Step 5: CLI.** `lode task claim <id> [--worktree <id>]`; default computed by new helper:

```go
// WorktreeIdentity returns the canonical worktree identity for dir:
// "<hostname>:<abs git worktree root>". Fails outside a git worktree.
func WorktreeIdentity(dir string) (string, error) {
	root, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	...
	host, err := os.Hostname()
	...
	return host + ":" + strings.TrimSpace(string(root)), nil
}
```

`--json` output carries `worktree`. Test with a temp git repo (`git init` in `t.TempDir()`).
- [x] **Step 6:** `go test ./...`; commit `git commit -m "Bind leases to worktree identity; drop session_id"`.

### Task 6: Claim concurrency race test

**Files:**
- Create: `internal/store/leases_race_test.go`

**Steps:**

- [x] **Step 1: Write the test** (spec 01 acceptance 4):

```go
// TestClaimRace fires N concurrent Claims at one ready task: exactly one
// wins; every loser gets ErrLeased; the task ends in_progress with exactly
// one active lease.
func TestClaimRace(t *testing.T) {
	s := OpenTestStore(t)
	// fixture: project p, actor a, one ready task
	const n = 16
	var wins, losses atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Claim(ctx, taskID, "a", fmt.Sprintf("h:/wt/%d", i), 0)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrLeased):
				losses.Add(1)
			default:
				t.Errorf("unexpected claim error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || losses.Load() != n-1 { t.Fatalf("wins=%d losses=%d", wins.Load(), losses.Load()) }
	// assert task state == in_progress and exactly one row in leases with released_at IS NULL
}
```

(Adapt fixture creation to the store's actual helpers from existing tests.)
- [x] **Step 2:** `go test ./internal/store/ -run TestClaimRace -count=5 -race` green. Commit.

### Task 7: Reopen — verify against spec

**Files:**
- Modify (if gaps): `internal/store/tasks.go`, `tasks_test.go`, `internal/cmd/task.go`, `internal/api/lifecycle.go`

**Steps:**

- [x] **Step 1:** Verify `legalTransitions` contains `done→ready` AND `abandoned→ready`; the reopen path emits event type `task.reopened` and a `state_log` row; `lode task reopen <id>` exists and hits it. Fix whatever is missing.
- [x] **Step 2:** Test: reopen from `done` and from `abandoned` both land in `ready` with a `task.reopened` event; reopen from `ready` → `ErrBadTransition`. Commit if changed.

### Task 8: Sweeper advisory lock

**Files:**
- Modify: `internal/store/leases.go` (`ExpireLeases`), `leases_test.go`, `internal/cmd/serve.go` (no change expected — ticker stays)

**Steps:**

- [x] **Step 1:** Wrap the sweep in an advisory lock so only one replica sweeps:

```go
// ExpireLeases: begin tx; SELECT pg_try_advisory_xact_lock(hashtext('worklode-sweeper'));
// if false -> return (0, nil) — another replica is sweeping.
```

Keep per-lease idempotent events (`lease-expired-<leaseID>`) exactly as they are — the lock is an optimization, idempotency remains the correctness backstop.
- [x] **Step 2:** Test: two concurrent `ExpireLeases` calls over a fixture of expired leases produce each `lease.expired` event exactly once (assert by event count). Commit.

### Task 9: Serve/migrate wiring, e2e, docs

**Files:**
- Modify: `internal/cmd/serve.go` (`--db <path>` → `--dsn` / env `LODE_DSN`), `internal/cmd/migrate.go` (same)
- Modify: `e2e/smoke_test.go` (fixture via `store.OpenTestStore`-equivalent — export a helper or duplicate the ephemeral-DB pattern; e2e is `//go:build e2e`)
- Modify: `README.md` (Quickstart: compose now brings up postgres; DSN config; delete litestream section), `Dockerfile` if it references /data
- Delete: `litestream.yml`

**Steps:**

- [x] **Step 1:** Replace the `--db` flag with `--dsn` (default `os.Getenv("LODE_DSN")`) in `serve` and `migrate`; error clearly when empty.
- [x] **Step 2:** Port `e2e/smoke_test.go` setup to the ephemeral-Postgres helper; `go test -tags e2e ./e2e/` green.
- [x] **Step 3:** README: Quickstart = `docker compose up -d` (postgres + migrate + server), `LODE_DSN` documented, litestream section removed, backup note pointing at CNPG.
- [x] **Step 4:** Full check: `go build ./... && go vet ./... && go test ./... && go test -tags e2e ./e2e/`. Commit.

### Task 10: Deploy — CNPG + migrate job

Load the `kubernetes` (CNPG section) and `golang-migrate:k8s-job` skills before this task. Follow the existing kustomize layout under `deploy/`.

**Files:**
- Create: `deploy/base/postgres.yaml` (CNPG `Cluster`, 1 instance for dev, storage per the kubernetes skill's CNPG defaults; a `Secret`/`Database` per CNPG idiom)
- Modify: `deploy/base/` server Deployment: `LODE_DSN` from the CNPG-generated secret; add migrate initContainer or Job per `golang-migrate:k8s-job`
- Delete: litestream sidecar/volume/config from manifests
- Modify: hzdev overlay as needed

**Steps:**

- [x] **Step 1:** Write the CNPG cluster + wire `LODE_DSN` + migrate job per the two skills; `kustomize build deploy/overlays/hzdev` (or the repo's equivalent) renders clean.
- [x] **Step 2:** Commit. **Human step (note in PR):** merge deploys to hzdev via flux; verify `/healthz` and a `lode task list` against `worklode.dev.sunstoneinstitute.ai` afterwards. Data loss on hzdev is accepted (dev only, fresh baseline).

---

## Acceptance criteria mapping (spec 01)

1. pgx + pool, suite on ephemeral Postgres → Tasks 3–4. 2. Baseline migration + round-trip → Tasks 2–3. 3. Worktree-bound leases, no session_id → Task 5. 4. Claim race → Task 6. 5. Claim takes caller-supplied candidate, no ranking → unchanged contract (Task 5). 6. Reopen → Task 7. 7. Event/provenance/idempotent sweep → Tasks 4, 8. 8. blocks gate + child_of cycles → ported as-is (Task 4); existing tests keep covering them.
