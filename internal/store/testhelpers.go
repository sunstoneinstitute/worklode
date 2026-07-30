package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
func OpenTestStore(t *testing.T) *Store {
	t.Helper()
	s := OpenUnmigratedTestStore(t)
	if err := s.Migrate(MigrationsDirForTests()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// OpenUnmigratedTestStore is OpenTestStore without the final Migrate call,
// for tests that need to stop at an intermediate schema version (e.g. to
// seed data before a specific migration runs) rather than jump to latest.
func OpenUnmigratedTestStore(t *testing.T) *Store {
	t.Helper()

	admin, err := sql.Open("pgx", TestDSN())
	if err == nil {
		err = admin.Ping()
	}
	if err != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("postgres unreachable at %s: %v", TestDSN(), err)
		}
		t.Fatalf("postgres unreachable at %s: %v", TestDSN(), err)
	}
	t.Cleanup(func() { admin.Close() })

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	dbName := "wl_test_" + hex.EncodeToString(buf)
	if _, err := admin.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", dbName)); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
	})

	u, err := url.Parse(TestDSN())
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	u.Path = "/" + dbName

	s, err := Open(u.String())
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

// MigrationsDirForTests returns the absolute path to deploy/base/migrations,
// resolved relative to this source file so it works no matter which
// package's test binary calls it. Tests that need a migrated database call
// Open then Migrate(store.MigrationsDirForTests()).
func MigrationsDirForTests() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "deploy", "base", "migrations")
}
