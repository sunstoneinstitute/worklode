package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestDSN returns the Postgres DSN test databases are created under.
// Default matches the docker-compose postgres service.
func TestDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
}

// OpenTestStore creates a uniquely named database by cloning a migrated
// template database (see ensureTemplate) and returns a Store bound to it.
// The database is dropped on cleanup. Skips the test if Postgres is
// unreachable and CI is not set.
func OpenTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	admin := adminConnForTest(t)

	tmpl, err := ensureTemplate(admin, MigrationsDirForTests())
	if err != nil {
		t.Fatalf("ensure template database: %v", err)
	}

	dbName := randomDBName(t, "wl_test_")
	if err := createFromTemplate(admin, dbName, tmpl); err != nil {
		t.Fatalf("create database %s from template %s: %v", dbName, tmpl, err)
	}
	t.Cleanup(func() { dropDatabase(t, admin, dbName) })

	return openTestDB(t, dbName, opts...)
}

// OpenUnmigratedTestStore is OpenTestStore without any migrations applied,
// for tests that need to stop at an intermediate schema version (e.g. to
// seed data before a specific migration runs) rather than jump to latest.
// It creates a plain empty database rather than cloning the template, since
// the template is already fully migrated.
func OpenUnmigratedTestStore(t *testing.T) *Store {
	t.Helper()
	admin := adminConnForTest(t)

	dbName := randomDBName(t, "wl_test_")
	if _, err := admin.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() { dropDatabase(t, admin, dbName) })

	return openTestDB(t, dbName)
}

// adminOnce/adminDB/adminErr memoize the admin pool for the lifetime of the
// test binary (WL-352): opening, pinging, and closing a fresh pool per test
// was a full Postgres handshake of pure waste — ~8ms idle and ~26ms under
// suite load — on each of the ~1200 OpenTestStore calls across the suite.
// The unreachable outcome is memoized too, so every test skips (or fails,
// under CI) the way the first one did. Nothing closes the pool; process
// exit does.
var (
	adminOnce sync.Once
	adminDB   *sql.DB
	adminErr  error
)

// adminConnForTest returns the process-wide connection pool to the admin
// (default) database used to create/drop per-test databases, skipping the
// test if Postgres is unreachable and CI is not set.
func adminConnForTest(t *testing.T) *sql.DB {
	t.Helper()
	adminOnce.Do(func() {
		adminDB, adminErr = sql.Open("pgx", TestDSN())
		if adminErr == nil {
			adminErr = adminDB.Ping()
		}
		if adminErr == nil {
			// Keep a few connections warm: database/sql's default of two
			// idle conns would close the rest after each burst of parallel
			// tests, reintroducing the handshake this pool exists to avoid.
			adminDB.SetMaxIdleConns(8)
		}
	})
	if adminErr != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("postgres unreachable at %s: %v", TestDSN(), adminErr)
		}
		t.Fatalf("postgres unreachable at %s: %v", TestDSN(), adminErr)
	}
	return adminDB
}

func randomDBName(t *testing.T, prefix string) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	return prefix + hex.EncodeToString(buf)
}

func dropDatabase(t *testing.T, admin *sql.DB, dbName string) {
	t.Helper()
	if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", dbName)); err != nil {
		t.Errorf("drop database %s: %v", dbName, err)
	}
}

func openTestDB(t *testing.T, dbName string, opts ...Option) *Store {
	t.Helper()
	u, err := url.Parse(TestDSN())
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	u.Path = "/" + dbName

	s, err := Open(u.String(), opts...)
	if err != nil {
		t.Fatalf("open %s: %v", dbName, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// DBForTests exposes the underlying connection pool so tests in other
// packages can make raw SQL assertions against a store's database.
func (s *Store) DBForTests() *sql.DB {
	return s.db
}

// moduleRoot/moduleRootErr cache this module's root, captured once from
// init() — before any test's TestMain or test body can os.Chdir elsewhere
// (internal/cmd's TestMain does exactly that, to isolate CLI config
// resolution). `go test` starts a package's test binary with that package's
// source directory as cwd, and every package sharing one process shares one
// initial cwd, so walking up from here to go.mod finds the module root no
// matter which package's test binary calls ModuleRootForTests. This
// deliberately does not use runtime.Caller, whose embedded file path is not
// a real filesystem path under -trimpath.
//
// This file is not a _test.go file — other packages' external test files
// import these helpers, which Go only allows from a package's regular
// (non-test) source — so init() runs in the real `lode` binary too. Gated on
// testing.Testing() and never panicking here (only lazily, in
// ModuleRootForTests, which production code never calls), so a real `lode`
// invocation outside a repo checkout is unaffected.
var (
	moduleRoot    string
	moduleRootErr error
)

func init() {
	if !testing.Testing() {
		return
	}
	moduleRoot, moduleRootErr = findModuleRoot()
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above initial cwd %s", dir)
		}
		dir = parent
	}
}

// ModuleRootForTests returns this module's root directory. Other packages'
// tests that need to run `go build`/`go run` against the repo (e.g. building
// cmd/lode) should use this instead of their own runtime.Caller-based path,
// which -trimpath breaks.
func ModuleRootForTests() string {
	if moduleRootErr != nil {
		panic(fmt.Sprintf("ModuleRootForTests: %v", moduleRootErr))
	}
	return moduleRoot
}

// MigrationsDirForTests returns the absolute path to deploy/base/migrations.
// Tests that need a migrated database call Open then
// Migrate(store.MigrationsDirForTests()).
func MigrationsDirForTests() string {
	return filepath.Join(ModuleRootForTests(), "deploy", "base", "migrations")
}

// Template-database machinery.
//
// Re-running every migration for every test is the dominant cost of
// OpenTestStore. Instead, a template database is migrated once and each
// test clones it with CREATE DATABASE ... TEMPLATE, which Postgres
// implements as a data-directory copy rather than DDL replay.
//
// templateOnce/templateResult/templateErr memoize the outcome for the
// lifetime of the test binary process: after the first OpenTestStore call,
// later calls never touch Postgres to find the template, they just reuse
// the cached name. This is process-local only — go test runs package
// binaries in parallel, so a Postgres advisory lock (see ensureTemplate)
// coordinates the actual build across processes.
var (
	templateOnce    sync.Once
	templateResult  string
	templateErr     error
	templateEnsures int // number of times the Once body ran; test instrumentation only
)

// sqlIdent quotes name as a Postgres identifier for use in statements that
// cannot use a bind parameter (e.g. CREATE/ALTER DATABASE).
func sqlIdent(name string) string {
	return `"` + name + `"`
}

// templateNameForMigrations derives a template database name from a hash of
// every file in migrationsDir (name and contents, sorted by name). Any
// change to the migrations — edited, added, or removed — produces a
// different name, so a stale template can never be cloned by mistake: the
// worst case is a one-time rebuild under a new name.
func templateNameForMigrations(migrationsDir string) (string, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return "", fmt.Errorf("read migrations dir %s: %w", migrationsDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return "", fmt.Errorf("read migration file %s: %w", name, err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
	}
	sum := h.Sum(nil)
	return "wl_tmpl_" + hex.EncodeToString(sum[:6]), nil
}

// advisoryLockKey derives a stable bigint lock key from the template name,
// so concurrent test binaries racing to build the same template serialize
// on the same Postgres advisory lock.
func advisoryLockKey(templateName string) int64 {
	sum := sha256.Sum256([]byte(templateName))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// ensureTemplate returns the name of a migrated template database matching
// migrationsDir, building it if necessary. Safe for concurrent processes:
// callers coordinate via a Postgres advisory lock keyed off the template
// name, so only one process ever builds a given template.
func ensureTemplate(admin *sql.DB, migrationsDir string) (string, error) {
	templateOnce.Do(func() {
		templateEnsures++
		templateResult, templateErr = buildOrFindTemplate(admin, migrationsDir)
	})
	return templateResult, templateErr
}

func buildOrFindTemplate(admin *sql.DB, migrationsDir string) (string, error) {
	name, err := templateNameForMigrations(migrationsDir)
	if err != nil {
		return "", err
	}

	// pg_advisory_lock is session-scoped, so the lock and unlock must run on
	// the same connection. Through the pool they might not — the per-test
	// pools this code used to get made that unlikely, but the shared admin
	// pool (WL-352) makes it routine — so pin one *sql.Conn for the lock's
	// lifetime. Everything else below can keep using the pool.
	lockKey := advisoryLockKey(name)
	ctx := context.Background()
	lockConn, err := admin.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("dedicated template-lock connection: %w", err)
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return "", fmt.Errorf("acquire template lock: %w", err)
	}
	defer lockConn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", lockKey)

	// Re-check existence inside the lock: another process may have built
	// this exact template (same migration hash) while we waited for it.
	exists, err := databaseExists(admin, name)
	if err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	if err := buildTemplate(admin, name, migrationsDir); err != nil {
		return "", err
	}
	return name, nil
}

func databaseExists(admin *sql.DB, name string) (bool, error) {
	var exists bool
	if err := admin.QueryRow(
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check database %s exists: %w", name, err)
	}
	return exists, nil
}

// buildTemplate migrates a fresh database under a temporary "_building_"
// name and only then renames it to the final template name. This makes the
// rename the commit point: if the process is killed mid-migration, the
// half-built database is never visible under the template name that other
// runs clone from, it just sits under its building name until reaped by the
// cleanup below (on the next successful build) or manually.
func buildTemplate(admin *sql.DB, name, migrationsDir string) error {
	buildBuf := make([]byte, 6)
	if _, err := rand.Read(buildBuf); err != nil {
		return fmt.Errorf("random building-database suffix: %w", err)
	}
	buildName := name + "_building_" + hex.EncodeToString(buildBuf)

	if _, err := admin.Exec("CREATE DATABASE " + sqlIdent(buildName)); err != nil {
		return fmt.Errorf("create building database %s: %w", buildName, err)
	}
	// Unconditional cleanup: once renameToTemplate succeeds, buildName no
	// longer exists and this is a no-op. It only bites when something above
	// failed or lost the build race, so no *_building_* database survives.
	defer admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", sqlIdent(buildName)))

	u, err := url.Parse(TestDSN())
	if err != nil {
		return fmt.Errorf("parse test DSN: %w", err)
	}
	u.Path = "/" + buildName

	s, err := Open(u.String())
	if err != nil {
		return fmt.Errorf("open building database %s: %w", buildName, err)
	}
	defer s.Close()
	// Store.Migrate() runs migrations over its own dedicated connection and
	// closes it before returning (see newMigrate in store.go), so no
	// connection to buildName lingers here for the rename below to trip
	// over.
	if err := s.Migrate(migrationsDir); err != nil {
		return fmt.Errorf("migrate building database %s: %w", buildName, err)
	}

	return renameToTemplate(admin, buildName, name)
}

func renameToTemplate(admin *sql.DB, buildName, name string) error {
	// We hold the advisory lock for the whole build, so this should never
	// find a winner already in place; kept as a defensive check in case
	// ensureTemplate is ever called outside that lock.
	exists, err := databaseExists(admin, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	// ALTER DATABASE ... RENAME, like CREATE DATABASE ... TEMPLATE, is
	// rejected (SQLSTATE 55006) while anything is connected to buildName.
	// m.Close() above closes both the migration driver's dedicated
	// connection and the pool, but Postgres can take a moment to notice the
	// client hung up and free the backend, so this needs the same
	// busy-retry as createFromTemplate.
	stmt := fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", sqlIdent(buildName), sqlIdent(name))
	if err := execWithBusyRetry(admin, stmt); err != nil {
		return fmt.Errorf("rename %s to %s: %w", buildName, name, err)
	}
	return nil
}

// createFromTemplate clones dbName from template. A straggler connection to
// the template (e.g. a migration pool from another process's build that
// hasn't fully closed yet) makes Postgres return SQLSTATE 55006; retry
// briefly rather than failing the whole test run over a timing race.
func createFromTemplate(admin *sql.DB, dbName, template string) error {
	stmt := fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", sqlIdent(dbName), sqlIdent(template))
	return execWithBusyRetry(admin, stmt)
}

// execWithBusyRetry runs stmt, retrying with a short backoff if Postgres
// reports SQLSTATE 55006 (source database is being accessed by other
// users) — the error CREATE DATABASE ... TEMPLATE and ALTER DATABASE ...
// RENAME both return when a connection to the source hasn't fully closed
// yet.
func execWithBusyRetry(admin *sql.DB, stmt string) error {
	const (
		attempts = 5
		backoff  = 50 * time.Millisecond
	)
	var lastErr error
	for i := 0; i < attempts; i++ {
		_, err := admin.Exec(stmt)
		if err == nil {
			return nil
		}
		lastErr = err
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55006" {
			return err
		}
		time.Sleep(backoff * time.Duration(i+1))
	}
	return lastErr
}

// AwaitCommitHorizon commits a marker event and waits until it has dropped
// below the commit horizon, which means every transaction that committed
// before it — everything the test just did — has too.
//
// Tests need it because DirtyProjects reads dirtiness from every visible row
// but only checkpoints through the horizon (WL-119): a change projects
// immediately, while the watermark passing it waits for pg_snapshot_xmin,
// which any unrelated transaction anywhere on the *instance* holds back for
// as long as it stays open. So "the projector has caught up and the next run
// is a no-op" is something a test waits for, never something one run
// establishes. Fails the test if the horizon has not moved within 20s, on the
// grounds that something is holding a transaction open far too long.
func AwaitCommitHorizon(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	marker, _, err := s.RecordEvent(ctx, "system",
		fmt.Sprintf("commit-horizon-marker-%d", time.Now().UnixNano()), "test.marker", nil, nil)
	if err != nil {
		t.Fatalf("record commit-horizon marker: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, err := s.EventLogHorizonID(ctx)
		if err != nil {
			t.Fatalf("event log horizon: %v", err)
		}
		if got >= marker {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("commit horizon still at %d after polling, want >= %d "+
				"(a long transaction elsewhere on the instance is holding it back)", got, marker)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// crewSeedSeq keeps the event external ids SeedCrewForTests writes unique
// within a test binary, since RecordEvent dedupes on (source, external_id).
var crewSeedSeq atomic.Int64

// SeedCrewForTests puts each actor on projectID's Crew as a plain member,
// through the same AddParticipant path production uses. Assignment requires
// Crew membership (see requireCrewMember), so every test that assigns or
// starts work — in this package and in internal/api, internal/cli and
// internal/cmd — needs the roster seeded first.
func SeedCrewForTests(t *testing.T, s *Store, projectID string, actorIDs ...string) {
	t.Helper()
	for _, id := range actorIDs {
		ext := fmt.Sprintf("seed-crew-%d", crewSeedSeq.Add(1))
		if _, _, err := s.RecordEvent(context.Background(), "cli", ext, "crew.member_added", nil,
			func(tx *sql.Tx, eventID int64) error {
				return AddParticipant(tx, s.nowFn(), projectID, id, "member", false, false, "", eventID)
			}); err != nil {
			t.Fatalf("seed crew member %s on project %s: %v", id, projectID, err)
		}
	}
}
