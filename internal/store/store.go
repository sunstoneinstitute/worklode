// Package store provides the Postgres-backed data store for the work tracker.
// It holds a pgx-backed database/sql connection pool; concurrency control is
// delegated to Postgres (transactions plus unique-constraint backstops).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Store wraps the Postgres connection pool.
type Store struct {
	db      *sql.DB
	dsn     string
	nowFn   func() time.Time
	metrics *storeMetrics
}

// Open opens a Postgres-backed store for the given postgres:// DSN. Callers
// are responsible for applying migrations (see Migrate) before relying on
// the schema being present — Open does not do this implicitly.
func Open(dsn string, opts ...Option) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	st := &Store{db: db, dsn: dsn, nowFn: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(st)
	}
	return st, nil
}

// SetNowFunc overrides the clock used for timestamps written by the store
// (e.g. events.received_at, lease expiry). Intended for tests that need to
// control time deterministically.
func (s *Store) SetNowFunc(f func() time.Time) {
	s.nowFn = f
}

// Now returns the store's current time. It respects SetNowFunc, so callers
// that need a timestamp for tx-scoped store functions (e.g. CreateTask) stay
// consistent with the store's own clock in tests.
func (s *Store) Now() time.Time {
	return s.nowFn()
}

// Migrate applies all pending migrations found as *.up.sql/*.down.sql files
// in migrationsPath. A database that is already up to date is not an error.
func (s *Store) Migrate(migrationsPath string) error {
	m, err := s.newMigrate(migrationsPath)
	if err != nil {
		return err
	}
	upErr := m.Up()
	srcErr, dbErr := m.Close()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", upErr)
	}
	if srcErr != nil || dbErr != nil {
		return fmt.Errorf("close migration driver: source=%v db=%v", srcErr, dbErr)
	}
	return nil
}

// MigrateDown reverts all applied migrations found as *.up.sql/*.down.sql
// files in migrationsPath. A database with nothing to revert is not an error.
func (s *Store) MigrateDown(migrationsPath string) error {
	m, err := s.newMigrate(migrationsPath)
	if err != nil {
		return err
	}
	downErr := m.Down()
	srcErr, dbErr := m.Close()
	if downErr != nil && !errors.Is(downErr, migrate.ErrNoChange) {
		return fmt.Errorf("revert migrations: %w", downErr)
	}
	if srcErr != nil || dbErr != nil {
		return fmt.Errorf("close migration driver: source=%v db=%v", srcErr, dbErr)
	}
	return nil
}

// newMigrate builds a migrate.Migrate instance over a *sql.DB dedicated to
// this migration run, rather than the Store's own pool (s.db). This matters
// because golang-migrate's pgx v5 driver Close() (database/pgx/v5.Postgres.
// Close) closes both its advisory-lock connection *and* whatever *sql.DB it
// was constructed with via WithInstance — confirmed by reading v4.19.1's
// database/pgx/v5/pgx.go, where Close does `p.conn.Close(); p.db.Close()`.
// Migrate/MigrateDown must call m.Close() to release the advisory-lock
// connection (otherwise it leaks: golang-migrate never returns it to the
// pool), but calling it against s.db would close the Store's pool along
// with it, breaking every caller that keeps using the Store afterward.
// Running migrations over their own short-lived *sql.DB sidesteps that:
// m.Close() closing it is exactly what we want.
func (s *Store) newMigrate(migrationsPath string) (*migrate.Migrate, error) {
	absPath, err := filepath.Abs(migrationsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve migrations path %s: %w", migrationsPath, err)
	}
	src, err := source.Open("file://" + absPath)
	if err != nil {
		return nil, fmt.Errorf("load migrations from %s: %w", absPath, err)
	}
	migrateDB, err := sql.Open("pgx", s.dsn)
	if err != nil {
		return nil, fmt.Errorf("open migration connection: %w", err)
	}
	migrateDB.SetMaxOpenConns(1)
	drv, err := migratepgx.WithInstance(migrateDB, &migratepgx.Config{})
	if err != nil {
		migrateDB.Close()
		return nil, fmt.Errorf("init migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("file", src, "pgx5", drv)
	if err != nil {
		migrateDB.Close()
		return nil, fmt.Errorf("init migrate: %w", err)
	}
	return m, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Tx runs fn inside a transaction, committing on nil and rolling back on error.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
